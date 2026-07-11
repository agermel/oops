package skills

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"oops/internal/logutil"

	"github.com/fsnotify/fsnotify"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"go.abhg.dev/goldmark/frontmatter"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// Skill 表示一个从 SKILL.md 文件加载的技能定义。
type Skill struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
	Content     string `json:"content"` // Markdown 正文（不含 frontmatter）
	Icon        string `json:"icon" yaml:"icon"`
	Label       string `json:"label" yaml:"label"`
	Color       string `json:"color" yaml:"color"`
	Enabled     bool   `json:"enabled" yaml:"enabled"`
}

// SkillStore 管理所有技能，从文件系统加载，支持热重载。
type SkillStore struct {
	mu        sync.RWMutex
	dir       string
	skills    map[string]*Skill // name → Skill
	watcher   *fsnotify.Watcher
	watchDone chan struct{}
	closeOnce sync.Once
}

// NewSkillStore 从指定目录加载所有 .md 文件作为 Skill。
// 启动 fsnotify watcher，文件变更时自动重载。
func NewSkillStore(dir string) (*SkillStore, error) {
	ss := &SkillStore{
		dir:    dir,
		skills: make(map[string]*Skill),
	}

	if err := ss.loadAll(); err != nil {
		return nil, err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logutil.Warn("skill: fsnotify unavailable, hot reload disabled", zap.Error(err))
	} else {
		if err := watcher.Add(dir); err != nil {
			logutil.Warn("skill: cannot watch dir", zap.String("dir", dir), zap.Error(err))
			watcher.Close()
		} else {
			ss.watcher = watcher
			ss.watchDone = make(chan struct{})
			go func() {
				defer close(ss.watchDone)
				ss.watchLoop()
			}()
		}
	}

	return ss, nil
}

// Get 返回指定名称的 Skill。不存在则返回 nil, false。
func (ss *SkillStore) Get(name string) (*Skill, bool) {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	s, ok := ss.skills[name]
	return s, ok
}

// List 返回所有 Skill（按名称排序）。
func (ss *SkillStore) List() []*Skill {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	result := make([]*Skill, 0, len(ss.skills))
	for _, s := range ss.skills {
		result = append(result, s)
	}
	return result
}

// Enabled 返回所有已启用的 Skill。
func (ss *SkillStore) Enabled() []*Skill {
	all := ss.List()
	var result []*Skill
	for _, s := range all {
		if s.Enabled {
			result = append(result, s)
		}
	}
	return result
}

// RenderAvailable 生成可用 Skill 的 XML 列表，用于注入系统提示词。
func (ss *SkillStore) RenderAvailable() string {
	enabled := ss.Enabled()
	described := make([]*Skill, 0, len(enabled))
	for _, s := range enabled {
		if s.Description != "" {
			described = append(described, s)
		}
	}

	if len(described) == 0 {
		return "No skills are currently available."
	}

	lines := []string{
		"Skills provide specialized instructions and workflows for specific tasks.",
		"Use the skill tool to load a skill when a task matches its description.",
		"<available_skills>",
	}
	for _, s := range described {
		lines = append(lines,
			"  <skill>",
			"    <name>"+s.Name+"</name>",
			"    <description>"+s.Description+"</description>",
			"  </skill>",
		)
	}
	lines = append(lines, "</available_skills>")
	return strings.Join(lines, "\n")
}

// Close 停止文件监听。
func (ss *SkillStore) Close() {
	ss.closeOnce.Do(func() {
		if ss.watcher == nil {
			return
		}
		_ = ss.watcher.Close()
		<-ss.watchDone
	})
}

// loadAll 加载 dir 下所有 .md 文件。
func (ss *SkillStore) loadAll() error {
	entries, err := os.ReadDir(ss.dir)
	if err != nil {
		return err
	}

	newSkills := make(map[string]*Skill)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		skill, err := loadSkillFile(filepath.Join(ss.dir, entry.Name()))
		if err != nil {
			logutil.Warn("skill: skip file", zap.String("file", entry.Name()), zap.Error(err))
			continue
		}
		newSkills[skill.Name] = skill
	}

	if len(newSkills) == 0 {
		logutil.Warn("skill: no skills loaded from", zap.String("dir", ss.dir))
	}

	ss.mu.Lock()
	ss.skills = newSkills
	ss.mu.Unlock()

	logutil.Infof("skill: loaded %d skills from %s", len(newSkills), ss.dir)
	return nil
}

// loadSkillFile 解析单个 Markdown 文件为 Skill。
// 文件格式：YAML frontmatter + Markdown body。
func loadSkillFile(path string) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	return parseSkillDocument(data)
}

func parseSkillDocument(data []byte) (*Skill, error) {
	var rawFrontmatter []byte
	format := frontmatter.YAML
	format.Unmarshal = func(raw []byte, target any) error {
		rawFrontmatter = raw
		return yaml.Unmarshal(raw, target)
	}

	markdown := goldmark.New(goldmark.WithExtensions(&frontmatter.Extender{
		Formats: []frontmatter.Format{format},
	}))
	ctx := parser.NewContext()
	markdown.Parser().Parse(text.NewReader(data), parser.WithContext(ctx))

	fm := frontmatter.Get(ctx)
	if fm == nil {
		return nil, &yaml.TypeError{Errors: []string{"name is required"}}
	}

	var skill Skill
	if err := fm.Decode(&skill); err != nil {
		return nil, err
	}
	if skill.Name == "" {
		return nil, &yaml.TypeError{Errors: []string{"name is required"}}
	}

	// 默认启用。
	// 如果 frontmatter 存在但没有 enabled 字段，yaml 默认为 false。
	// 检查原始 YAML 中是否显式写了 enabled。
	var fields map[string]any
	if err := fm.Decode(&fields); err == nil && fields != nil {
		if _, hasEnabled := fields["enabled"]; !hasEnabled {
			skill.Enabled = true
		}
	}

	skill.Content = strings.TrimSpace(string(skillBody(data, rawFrontmatter)))
	return &skill, nil
}

// skillBody 根据 Goldmark 已解析的 frontmatter 位置截取原始 Markdown 正文。
func skillBody(data, rawFrontmatter []byte) []byte {
	openingLineEnd := bytes.IndexByte(data, '\n')
	if openingLineEnd < 0 {
		return nil
	}

	closingLineStart := openingLineEnd + 1 + len(rawFrontmatter)
	if closingLineStart >= len(data) {
		return nil
	}
	closingLineEnd := bytes.IndexByte(data[closingLineStart:], '\n')
	if closingLineEnd < 0 {
		return nil
	}
	return data[closingLineStart+closingLineEnd+1:]
}

// watchLoop 监听文件变更并自动重载。
func (ss *SkillStore) watchLoop() {
	for {
		select {
		case event, ok := <-ss.watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Create) || event.Has(fsnotify.Write) || event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
				if strings.HasSuffix(event.Name, ".md") {
					logutil.Infof("skill: file changed, reloading %s", event.Name)
					if err := ss.loadAll(); err != nil {
						logutil.Error("skill: reload failed", zap.Error(err))
					}
				}
			}
		case err, ok := <-ss.watcher.Errors:
			if !ok {
				return
			}
			logutil.Error("skill: watcher error", zap.Error(err))
		}
	}
}
