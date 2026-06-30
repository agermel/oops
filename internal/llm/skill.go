package llm

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"oops/internal/logutil"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// Skill 表示一个从 SKILL.md 文件加载的技能定义。
type Skill struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
	Content     string `json:"content"`  // Markdown 正文（不含 frontmatter）
	Icon        string `json:"icon" yaml:"icon"`
	Label       string `json:"label" yaml:"label"`
	Color       string `json:"color" yaml:"color"`
	Enabled     bool   `json:"enabled" yaml:"enabled"`
}

// SkillStore 管理所有技能，从文件系统加载，支持热重载。
type SkillStore struct {
	mu      sync.RWMutex
	dir     string
	skills  map[string]*Skill // name → Skill
	watcher *fsnotify.Watcher
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
			go ss.watchLoop()
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
	if ss.watcher != nil {
		ss.watcher.Close()
	}
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

	fm, body, err := parseFrontmatter(data)
	if err != nil {
		return nil, err
	}

	var skill Skill
	if err := yaml.Unmarshal(fm, &skill); err != nil {
		return nil, err
	}
	if skill.Name == "" {
		return nil, &yaml.TypeError{Errors: []string{"name is required"}}
	}
	if skill.Enabled && fm == nil {
		// 新格式无 frontmatter → 不做特殊处理
	}
	// 默认启用。
	if fm != nil {
		// 如果 frontmatter 存在但没有 enabled 字段，yaml 默认为 false。
		// 检查原始 YAML 中是否显式写了 enabled。
		var raw map[string]any
		yaml.Unmarshal(fm, &raw)
		if raw != nil {
			if _, hasEnabled := raw["enabled"]; !hasEnabled {
				skill.Enabled = true // 缺失时默认启用
			}
		}
	}

	skill.Content = strings.TrimSpace(string(body))
	return &skill, nil
}

// parseFrontmatter 解析 YAML frontmatter，返回 YAML 字节和正文内容。
// Frontmatter 以 --- 开头和结尾。
func parseFrontmatter(data []byte) ([]byte, []byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))

	// 检查第一行是否为 ---
	if !scanner.Scan() {
		return nil, nil, nil // 空文件
	}
	if strings.TrimSpace(scanner.Text()) != "---" {
		// 无 frontmatter，整个文件为正文。
		return nil, data, nil
	}

	// 读取 frontmatter 直到下一个 ---
	var fmLines []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		fmLines = append(fmLines, line)
	}

	// 剩余部分为正文。
	bodyStart := 0
	for i, b := range data {
		if b == '\n' {
			bodyStart = i + 1
		}
	}
	// 跳过 frontmatter 部分。
	dashesSeen := 0
	bodyStart = 0
	lines := bytes.Split(data, []byte{'\n'})
	for i, line := range lines {
		trimmed := strings.TrimSpace(string(line))
		if trimmed == "---" {
			dashesSeen++
			if dashesSeen == 2 {
				if i+1 < len(lines) {
					bodyStart = i + 1
				}
				break
			}
		}
	}

	var body []byte
	if bodyStart > 0 && bodyStart < len(lines) {
		body = bytes.Join(lines[bodyStart:], []byte{'\n'})
	}

	fm := []byte(strings.Join(fmLines, "\n"))
	return fm, body, nil
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
