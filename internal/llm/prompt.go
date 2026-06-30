package llm

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"oops/internal/logutil"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// PromptStore 管理从 YAML 文件加载的系统提示词，支持文件变更热加载。
type PromptStore struct {
	mu      sync.RWMutex
	prompts map[string]string // name → content
	dir     string
	watcher *fsnotify.Watcher
}

// NewPromptStore 从指定目录加载所有 .yaml 文件作为 prompt。
// 每个 YAML 文件的顶层 key 即为 prompt 名称（如 "system"、"diagnose"）。
// 启动 fsnotify watcher，文件变更时自动重载。
func NewPromptStore(dir string) (*PromptStore, error) {
	ps := &PromptStore{
		prompts: make(map[string]string),
		dir:     dir,
	}

	if err := ps.loadAll(); err != nil {
		return nil, err
	}

	// 设置文件监听。
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logutil.Warn("prompt: fsnotify unavailable, hot reload disabled", zap.Error(err))
	} else {
		if err := watcher.Add(dir); err != nil {
			logutil.Warn("prompt: cannot watch dir", zap.String("dir", dir), zap.Error(err))
			watcher.Close()
		} else {
			ps.watcher = watcher
			go ps.watchLoop()
		}
	}

	return ps, nil
}

// Get 返回指定名称的 prompt 内容。若不存在则返回空字符串。
func (ps *PromptStore) Get(name string) string {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.prompts[name]
}

// Resolve 根据用户问题内容选择合适的 prompt。
// 返回 prompt 内容和实际使用的名称。
func (ps *PromptStore) Resolve(question string) (content string, name string) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	q := strings.ToLower(question)

	// 故障诊断场景。
	diagnoseKW := []string{"故障", "报错", "异常", "挂了", "不行了", "crash", "error", "fail",
		"出问题", "不工作", "起不来", "连不上", "超时", "timeout", "慢", "排查", "诊断"}
	for _, kw := range diagnoseKW {
		if strings.Contains(q, kw) {
			if c, ok := ps.prompts["diagnose"]; ok {
				return c, "diagnose"
			}
			break
		}
	}

	// 日常巡检场景。
	inspectKW := []string{"巡检", "检查一下", "怎么样", "状态", "overview", "概况", "概览", "汇总", "报告"}
	for _, kw := range inspectKW {
		if strings.Contains(q, kw) {
			if c, ok := ps.prompts["inspect"]; ok {
				return c, "inspect"
			}
			break
		}
	}

	// 默认。
	if c, ok := ps.prompts["system"]; ok {
		return c, "system"
	}
	return "", ""
}

// Close 停止文件监听。
func (ps *PromptStore) Close() {
	if ps.watcher != nil {
		ps.watcher.Close()
	}
}

// loadAll 加载 dir 下所有 .yaml 文件。
func (ps *PromptStore) loadAll() error {
	entries, err := os.ReadDir(ps.dir)
	if err != nil {
		return err
	}

	newPrompts := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		filePrompts, err := loadPromptFile(filepath.Join(ps.dir, entry.Name()))
		if err != nil {
			logutil.Warn("prompt: skip file", zap.String("file", entry.Name()), zap.Error(err))
			continue
		}
		for k, v := range filePrompts {
			newPrompts[k] = v
		}
	}

	if len(newPrompts) == 0 {
		logutil.Warn("prompt: no prompts loaded from", zap.String("dir", ps.dir))
	}

	ps.mu.Lock()
	ps.prompts = newPrompts
	ps.mu.Unlock()

	logutil.Infof("prompt: loaded %d prompts from %s", len(newPrompts), ps.dir)
	return nil
}

// loadPromptFile 解析单个 YAML 文件，返回 key → content 映射。
func loadPromptFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var raw map[string]string
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	return raw, nil
}

// watchLoop 监听文件变更并自动重载。
func (ps *PromptStore) watchLoop() {
	for {
		select {
		case event, ok := <-ps.watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Create) || event.Has(fsnotify.Write) {
				if strings.HasSuffix(event.Name, ".yaml") {
					logutil.Infof("prompt: file changed, reloading %s", event.Name)
					if err := ps.loadAll(); err != nil {
						logutil.Error("prompt: reload failed", zap.Error(err))
					}
				}
			}
		case err, ok := <-ps.watcher.Errors:
			if !ok {
				return
			}
			logutil.Error("prompt: watcher error", zap.Error(err))
		}
	}
}
