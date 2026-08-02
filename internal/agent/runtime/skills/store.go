package skills

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf16"

	"oops/internal/logutil"

	"github.com/fsnotify/fsnotify"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/google/uuid"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"go.abhg.dev/goldmark/frontmatter"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

const (
	maxNameLength        = 64
	maxDescriptionLength = 1024

	SkillDiagnosticWarning   = "warning"
	SkillDiagnosticCollision = "collision"
)

var ignoreFileNames = [...]string{".gitignore", ".ignore", ".fdignore"}

// SkillDiagnosticCode identifies a stable skill-loading failure category.
type SkillDiagnosticCode string

const (
	SkillDiagnosticFileInfoFailed  SkillDiagnosticCode = "file_info_failed"
	SkillDiagnosticListFailed      SkillDiagnosticCode = "list_failed"
	SkillDiagnosticReadFailed      SkillDiagnosticCode = "read_failed"
	SkillDiagnosticParseFailed     SkillDiagnosticCode = "parse_failed"
	SkillDiagnosticInvalidMetadata SkillDiagnosticCode = "invalid_metadata"
	SkillDiagnosticNameCollision   SkillDiagnosticCode = "name_collision"
)

var skillNamePattern = regexp.MustCompile(`^[a-z0-9-]+$`)

var (
	ErrSkillNotFound        = errors.New("skill not found")
	ErrSkillChanged         = errors.New("skill changed")
	ErrSkillPathOutsideRoot = errors.New("skill file path is outside store root")
)

// Skill represents a skill loaded from a Markdown document.
type Skill struct {
	Name                   string `json:"name" yaml:"name"`
	Description            string `json:"description" yaml:"description"`
	Content                string `json:"content" yaml:"-"`
	FilePath               string `json:"filePath" yaml:"-"`
	DisableModelInvocation bool   `json:"disableModelInvocation" yaml:"disable-model-invocation"`
	Icon                   string `json:"icon" yaml:"icon"`
	Label                  string `json:"label" yaml:"label"`
	Color                  string `json:"color" yaml:"color"`
	Enabled                bool   `json:"enabled" yaml:"enabled"`
	metadata               map[string]any
}

// SkillCollision identifies the selected and discarded files for one skill name.
type SkillCollision struct {
	ResourceType string `json:"resourceType"`
	Name         string `json:"name"`
	WinnerPath   string `json:"winnerPath"`
	LoserPath    string `json:"loserPath"`
}

// SkillDiagnostic describes a diagnostic produced while loading skills.
type SkillDiagnostic struct {
	Type      string              `json:"type"`
	Code      SkillDiagnosticCode `json:"code,omitempty"`
	Message   string              `json:"message"`
	Path      string              `json:"path"`
	Collision *SkillCollision     `json:"collision,omitempty"`
}

// Store loads skills from a directory and watches it for changes.
type Store struct {
	reloadMu    sync.Mutex
	mu          sync.RWMutex
	dir         string
	skills      map[string]*Skill
	diagnostics []SkillDiagnostic
	watcher     *fsnotify.Watcher
	watchDone   chan struct{}
	fileRoot    *os.Root
	rootBound   bool
	closeOnce   sync.Once
}

// NewStore loads all skills from dir and starts recursive hot reloading.
func NewStore(dir string) (*Store, error) {
	absoluteDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absoluteDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil && !info.IsDir() {
		return nil, fmt.Errorf("skill path %q is not a directory", absoluteDir)
	}

	store := &Store{
		dir:         absoluteDir,
		skills:      make(map[string]*Skill),
		diagnostics: make([]SkillDiagnostic, 0),
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logutil.Warn("skill: fsnotify unavailable, hot reload disabled", zap.Error(err))
		if err := store.loadAll(); err != nil {
			return nil, err
		}
		return store, nil
	}
	anchor := nearestExistingDir(filepath.Dir(absoluteDir))
	if err := watcher.Add(anchor); err != nil {
		logutil.Warn("skill: cannot watch dir", zap.String("dir", absoluteDir), zap.Error(err))
		_ = watcher.Close()
		if err := store.loadAll(); err != nil {
			return nil, err
		}
		return store, nil
	}

	store.watcher = watcher
	store.watchDone = make(chan struct{})
	if err := store.loadAll(); err != nil {
		_ = watcher.Close()
		store.closeFileRoot()
		return nil, err
	}
	go func() {
		defer close(store.watchDone)
		store.watchLoop()
	}()
	return store, nil
}

// Get returns an isolated copy of the skill with the given name.
func (s *Store) Get(name string) (*Skill, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	skill, ok := s.skills[name]
	return cloneSkill(skill), ok
}

// List returns isolated skill copies sorted by name.
func (s *Store) List() []*Skill {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Skill, 0, len(s.skills))
	for _, skill := range s.skills {
		result = append(result, cloneSkill(skill))
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// Enabled returns isolated copies of all enabled skills.
func (s *Store) Enabled() []*Skill {
	all := s.List()
	result := make([]*Skill, 0, len(all))
	for _, skill := range all {
		if skill.Enabled {
			result = append(result, skill)
		}
	}
	return result
}

// Diagnostics returns isolated diagnostics from the latest complete reload.
func (s *Store) Diagnostics() []SkillDiagnostic {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]SkillDiagnostic, len(s.diagnostics))
	for index := range s.diagnostics {
		result[index] = cloneDiagnostic(s.diagnostics[index])
	}
	return result
}

// Update atomically replaces one complete skill and retains store ownership.
func (s *Store) Update(skill *Skill) bool {
	if skill == nil || skill.Name == "" {
		return false
	}
	updated := cloneSkill(skill)

	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.skills[skill.Name]; !ok {
		return false
	}
	s.skills[skill.Name] = updated
	return true
}

// Save atomically persists one loaded skill and publishes the same snapshot.
func (s *Store) Save(skill *Skill) error {
	if skill == nil || skill.Name == "" {
		return ErrSkillNotFound
	}
	updated := cloneSkill(skill)
	data, err := marshalSkillFile(updated)
	if err != nil {
		return err
	}

	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	s.mu.RLock()
	current, ok := s.skills[updated.Name]
	current = cloneSkill(current)
	s.mu.RUnlock()
	if !ok {
		return ErrSkillNotFound
	}
	if current.FilePath != updated.FilePath {
		return ErrSkillChanged
	}
	if err := s.writeFile(current.FilePath, data); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	latest, ok := s.skills[updated.Name]
	if !ok || latest.FilePath != current.FilePath {
		return ErrSkillChanged
	}
	updated.FilePath = current.FilePath
	s.skills[updated.Name] = updated
	return nil
}

// Remove deletes one loaded skill file and removes it from the published snapshot.
func (s *Store) Remove(name string) error {
	if name == "" {
		return ErrSkillNotFound
	}
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	s.mu.RLock()
	current, ok := s.skills[name]
	current = cloneSkill(current)
	s.mu.RUnlock()
	if !ok {
		return ErrSkillNotFound
	}

	parent, base, err := s.openFileParent(current.FilePath)
	if err != nil {
		return err
	}
	defer parent.Close()
	if err := parent.Remove(base); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	latest, ok := s.skills[name]
	if !ok || latest.FilePath != current.FilePath {
		return ErrSkillChanged
	}
	delete(s.skills, name)
	return nil
}

// RenderAvailable formats model-invocable skills for inclusion in a system prompt.
func (s *Store) RenderAvailable() string {
	enabled := s.Enabled()
	available := make([]Skill, 0, len(enabled))
	for _, skill := range enabled {
		if skill != nil {
			available = append(available, *skill)
		}
	}
	return FormatAvailable(available)
}

func marshalSkillFile(skill *Skill) ([]byte, error) {
	fields := cloneMetadata(skill.metadata)
	if fields == nil {
		fields = make(map[string]any)
	}
	fields["name"] = skill.Name
	fields["description"] = skill.Description
	setOptionalMetadata(fields, "disable-model-invocation", skill.DisableModelInvocation)
	setOptionalMetadata(fields, "icon", skill.Icon)
	setOptionalMetadata(fields, "label", skill.Label)
	setOptionalMetadata(fields, "color", skill.Color)
	if skill.Enabled {
		delete(fields, "enabled")
	} else {
		fields["enabled"] = false
	}

	frontmatter, err := yaml.Marshal(fields)
	if err != nil {
		return nil, err
	}
	var content strings.Builder
	content.WriteString("---\n")
	content.Write(frontmatter)
	content.WriteString("---\n")
	content.WriteString(skill.Content)
	content.WriteByte('\n')
	return []byte(content.String()), nil
}

func (s *Store) writeFile(path string, data []byte) error {
	parent, base, err := s.openFileParent(path)
	if err != nil {
		return err
	}
	defer parent.Close()

	tempName := "." + base + ".tmp-" + uuid.NewString()
	temp, err := parent.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		_ = temp.Close()
		if !committed {
			_ = parent.Remove(tempName)
		}
	}()
	if err := temp.Chmod(0o644); err != nil {
		return err
	}
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := parent.Rename(tempName, base); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) openFileParent(path string) (*os.Root, string, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, "", err
	}
	relativePath, err := filepath.Rel(s.dir, absolutePath)
	if err != nil {
		return nil, "", err
	}
	if relativePath == "." || filepath.IsAbs(relativePath) || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
		return nil, "", fmt.Errorf("%w: %q", ErrSkillPathOutsideRoot, absolutePath)
	}

	if s.fileRoot == nil || !s.rootMatchesCurrent() {
		return nil, "", fmt.Errorf("%w: %q", ErrSkillPathOutsideRoot, absolutePath)
	}
	parent, err := s.fileRoot.OpenRoot(filepath.Dir(relativePath))
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrSkillPathOutsideRoot, err)
	}
	return parent, filepath.Base(relativePath), nil
}

// Close stops file watching and waits for the watcher goroutine to exit.
func (s *Store) Close() {
	s.closeOnce.Do(func() {
		if s.watcher != nil {
			_ = s.watcher.Close()
			<-s.watchDone
		}
		s.reloadMu.Lock()
		defer s.reloadMu.Unlock()
		s.closeFileRoot()
	})
}

func (s *Store) loadAll() error {
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()

	result, err := s.discoverSkills()
	if s.watcher != nil {
		s.syncWatchDirs(result.watchDirs)
		second, secondErr := s.discoverSkills()
		s.syncWatchDirs(second.watchDirs)
		result, err = second, secondErr
	}
	if err != nil {
		return err
	}
	loadedCount := len(result.skills)

	s.mu.Lock()
	s.skills = result.skills
	s.diagnostics = result.diagnostics
	s.mu.Unlock()

	for _, diagnostic := range result.diagnostics {
		logutil.Warn("skill: load warning",
			zap.String("code", string(diagnostic.Code)),
			zap.String("path", diagnostic.Path),
			zap.String("message", diagnostic.Message),
		)
	}
	if loadedCount == 0 {
		logutil.Warn("skill: no skills loaded from", zap.String("dir", s.dir))
	}
	logutil.Infof("skill: loaded %d skills from %s", loadedCount, s.dir)
	return nil
}

type skillDiscovery struct {
	skills      map[string]*Skill
	diagnostics []SkillDiagnostic
	watchDirs   map[string]struct{}
	realFiles   map[string]struct{}
}

type ignoreMatchers struct {
	patterns []gitignore.Pattern
}

func (s *Store) discoverSkills() (skillDiscovery, error) {
	result := skillDiscovery{
		skills:      make(map[string]*Skill),
		diagnostics: make([]SkillDiagnostic, 0),
		watchDirs:   make(map[string]struct{}),
		realFiles:   make(map[string]struct{}),
	}
	result.watchDirs[nearestExistingDir(filepath.Dir(s.dir))] = struct{}{}

	info, err := os.Stat(s.dir)
	if os.IsNotExist(err) {
		s.closeFileRoot()
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if !info.IsDir() {
		s.closeFileRoot()
		err := fmt.Errorf("skill path %q is not a directory", s.dir)
		result.diagnostics = append(result.diagnostics, skillDiagnostic(SkillDiagnosticFileInfoFailed, s.dir, err))
		return result, nil
	}
	if err := s.bindFileRoot(info); err != nil {
		result.diagnostics = append(result.diagnostics, skillDiagnostic(SkillDiagnosticFileInfoFailed, s.dir, err))
		return result, nil
	}

	visited := make(map[string]struct{})
	ignores := &ignoreMatchers{}
	err = loadSkillsFromDir(s.dir, s.dir, true, true, visited, ignores, &result)
	return result, err
}

func loadSkillsFromDir(
	rootDir string,
	dir string,
	includeRootFiles bool,
	root bool,
	visited map[string]struct{},
	ignores *ignoreMatchers,
	result *skillDiscovery,
) error {
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		result.diagnostics = append(result.diagnostics, skillDiagnostic(SkillDiagnosticFileInfoFailed, dir, err))
		if root {
			return err
		}
		return nil
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		result.diagnostics = append(result.diagnostics, skillDiagnostic(SkillDiagnosticFileInfoFailed, dir, err))
		if root {
			return err
		}
		return nil
	}
	if _, ok := visited[canonical]; ok {
		return nil
	}
	visited[canonical] = struct{}{}
	result.watchDirs[dir] = struct{}{}
	ignores.addFromDir(rootDir, dir, &result.diagnostics)

	entries, err := os.ReadDir(dir)
	if err != nil {
		result.diagnostics = append(result.diagnostics, skillDiagnostic(SkillDiagnosticListFailed, dir, err))
		if root {
			return err
		}
		return nil
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.Name() != "SKILL.md" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		isFile, _, infoErr := inspectSkillPath(path)
		if infoErr != nil {
			result.diagnostics = append(result.diagnostics, skillDiagnostic(SkillDiagnosticFileInfoFailed, path, infoErr))
			continue
		}
		if !isFile {
			continue
		}
		if ignores.ignores(relativeSkillPath(rootDir, path)) {
			continue
		}
		skill, fileDiagnostics := loadSkillFile(path)
		result.diagnostics = append(result.diagnostics, fileDiagnostics...)
		if skill != nil {
			addLoadedSkill(skill, result)
		}
		return nil
	}

	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" {
			continue
		}
		path := filepath.Join(dir, name)
		isFile, isDir, infoErr := inspectSkillPath(path)
		if infoErr != nil {
			result.diagnostics = append(result.diagnostics, skillDiagnostic(SkillDiagnosticFileInfoFailed, path, infoErr))
			continue
		}
		ignorePath := relativeSkillPath(rootDir, path)
		if isDir {
			ignorePath += "/"
		}
		if ignores.ignores(ignorePath) {
			continue
		}
		if isDir {
			if err := loadSkillsFromDir(rootDir, path, false, false, visited, ignores, result); err != nil {
				return err
			}
			continue
		}
		if !isFile || !includeRootFiles || !strings.HasSuffix(name, ".md") {
			continue
		}
		skill, fileDiagnostics := loadSkillFile(path)
		result.diagnostics = append(result.diagnostics, fileDiagnostics...)
		if skill != nil {
			addLoadedSkill(skill, result)
		}
	}
	return nil
}

func (m *ignoreMatchers) addFromDir(rootDir, dir string, diagnostics *[]SkillDiagnostic) {
	relativeDir := relativeSkillPath(rootDir, dir)
	if relativeDir == "." {
		relativeDir = ""
	}
	for _, name := range ignoreFileNames {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			*diagnostics = append(*diagnostics, skillDiagnostic(SkillDiagnosticFileInfoFailed, path, err))
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			*diagnostics = append(*diagnostics, skillDiagnostic(SkillDiagnosticReadFailed, path, err))
			continue
		}
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		domain := splitIgnorePath(relativeDir)
		for _, line := range lines {
			pattern, ok := compileIgnoreRule(line, domain)
			if !ok {
				continue
			}
			m.patterns = append(m.patterns, pattern)
		}
	}
}

func compileIgnoreRule(line string, domain []string) (gitignore.Pattern, bool) {
	line = strings.TrimRight(line, "\r")
	if strings.HasPrefix(line, "#") {
		return nil, false
	}
	if strings.TrimSpace(line) == "" {
		return nil, false
	}
	if line == "!" {
		return nil, false
	}
	return gitignore.ParsePattern(line, domain), true
}

func (m *ignoreMatchers) ignores(path string) bool {
	path = filepath.ToSlash(path)
	isDir := strings.HasSuffix(path, "/")
	return gitignore.NewMatcher(m.patterns).Match(splitIgnorePath(strings.TrimSuffix(path, "/")), isDir)
}

func splitIgnorePath(path string) []string {
	path = filepath.ToSlash(path)
	if path == "" || path == "." {
		return nil
	}
	return strings.Split(path, "/")
}

func relativeSkillPath(rootDir, path string) string {
	relative, err := filepath.Rel(rootDir, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relative)
}

func addLoadedSkill(skill *Skill, result *skillDiscovery) {
	canonical, err := filepath.EvalSymlinks(skill.FilePath)
	if err != nil {
		canonical = skill.FilePath
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		canonical = skill.FilePath
	}
	if _, ok := result.realFiles[canonical]; ok {
		return
	}
	result.realFiles[canonical] = struct{}{}

	existing, ok := result.skills[skill.Name]
	if !ok {
		result.skills[skill.Name] = skill
		return
	}
	result.diagnostics = append(result.diagnostics, SkillDiagnostic{
		Type:    SkillDiagnosticCollision,
		Code:    SkillDiagnosticNameCollision,
		Message: fmt.Sprintf("name %q collision", skill.Name),
		Path:    skill.FilePath,
		Collision: &SkillCollision{
			ResourceType: "skill",
			Name:         skill.Name,
			WinnerPath:   existing.FilePath,
			LoserPath:    skill.FilePath,
		},
	})
}

func inspectSkillPath(path string) (isFile, isDir bool, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, false, err
	}
	return info.Mode().IsRegular(), info.IsDir(), nil
}

func loadSkillFile(path string) (*Skill, []SkillDiagnostic) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, []SkillDiagnostic{skillDiagnostic(SkillDiagnosticFileInfoFailed, path, err)}
	}
	data, err := os.ReadFile(absolutePath)
	if err != nil {
		return nil, []SkillDiagnostic{skillDiagnostic(SkillDiagnosticReadFailed, absolutePath, err)}
	}

	fields, body, err := parseDocument(data)
	if err != nil {
		return nil, []SkillDiagnostic{skillDiagnostic(SkillDiagnosticParseFailed, absolutePath, err)}
	}

	parentDirName := filepath.Base(filepath.Dir(absolutePath))
	name := metadataString(fields, "name")
	if name == "" {
		name = parentDirName
	}
	description := metadataString(fields, "description")
	enabled := true
	if value, ok := fields["enabled"].(bool); ok {
		enabled = value
	}

	skill := &Skill{
		Name:                   name,
		Description:            description,
		Content:                body,
		FilePath:               absolutePath,
		DisableModelInvocation: metadataBool(fields, "disable-model-invocation"),
		Icon:                   metadataString(fields, "icon"),
		Label:                  metadataString(fields, "label"),
		Color:                  metadataString(fields, "color"),
		Enabled:                enabled,
		metadata:               cloneMetadata(fields),
	}

	diagnostics := validateSkillMetadata(skill, parentDirName, absolutePath)
	if strings.TrimSpace(description) == "" {
		return nil, diagnostics
	}
	return skill, diagnostics
}

func parseDocument(data []byte) (map[string]any, string, error) {
	normalized := strings.ReplaceAll(string(data), "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	document := []byte(normalized)

	var rawFrontmatter []byte
	format := frontmatter.YAML
	format.Unmarshal = func(raw []byte, target any) error {
		rawFrontmatter = bytes.Clone(raw)
		return yaml.Unmarshal(raw, target)
	}
	markdown := goldmark.New(goldmark.WithExtensions(&frontmatter.Extender{
		Formats: []frontmatter.Format{format},
	}))
	ctx := parser.NewContext()
	markdown.Parser().Parse(text.NewReader(document), parser.WithContext(ctx))

	metadata := frontmatter.Get(ctx)
	if metadata == nil {
		return map[string]any{}, strings.TrimSpace(normalized), nil
	}
	fields := make(map[string]any)
	if err := metadata.Decode(&fields); err != nil {
		return nil, "", err
	}
	body, ok := documentBody(document, rawFrontmatter)
	if !ok {
		return map[string]any{}, strings.TrimSpace(normalized), nil
	}
	return fields, strings.TrimSpace(string(body)), nil
}

func documentBody(document, rawFrontmatter []byte) ([]byte, bool) {
	openingLineEnd := bytes.IndexByte(document, '\n')
	if openingLineEnd < 0 {
		return nil, false
	}
	closingLineStart := openingLineEnd + 1 + len(rawFrontmatter)
	if closingLineStart >= len(document) {
		return nil, false
	}
	closingLineEnd := bytes.IndexByte(document[closingLineStart:], '\n')
	if closingLineEnd < 0 {
		return nil, true
	}
	return document[closingLineStart+closingLineEnd+1:], true
}

func metadataString(fields map[string]any, key string) string {
	value, _ := fields[key].(string)
	return value
}

func metadataBool(fields map[string]any, key string) bool {
	value, _ := fields[key].(bool)
	return value
}

func validateSkillMetadata(skill *Skill, parentDirName, path string) []SkillDiagnostic {
	messages := make([]string, 0)
	if skill.Name != parentDirName {
		messages = append(messages, fmt.Sprintf("name %q does not match parent directory %q", skill.Name, parentDirName))
	}
	nameLength := utf16Length(skill.Name)
	if nameLength > maxNameLength {
		messages = append(messages, fmt.Sprintf("name exceeds %d characters (%d)", maxNameLength, nameLength))
	}
	if !skillNamePattern.MatchString(skill.Name) {
		messages = append(messages, "name contains invalid characters (must be lowercase a-z, 0-9, hyphens only)")
	}
	if strings.HasPrefix(skill.Name, "-") || strings.HasSuffix(skill.Name, "-") {
		messages = append(messages, "name must not start or end with a hyphen")
	}
	if strings.Contains(skill.Name, "--") {
		messages = append(messages, "name must not contain consecutive hyphens")
	}

	descriptionLength := utf16Length(skill.Description)
	if strings.TrimSpace(skill.Description) == "" {
		messages = append(messages, "description is required")
	} else if descriptionLength > maxDescriptionLength {
		messages = append(messages, fmt.Sprintf("description exceeds %d characters (%d)", maxDescriptionLength, descriptionLength))
	}

	diagnostics := make([]SkillDiagnostic, 0, len(messages))
	for _, message := range messages {
		diagnostics = append(diagnostics, SkillDiagnostic{
			Type:    SkillDiagnosticWarning,
			Code:    SkillDiagnosticInvalidMetadata,
			Message: message,
			Path:    path,
		})
	}
	return diagnostics
}

func skillDiagnostic(code SkillDiagnosticCode, path string, err error) SkillDiagnostic {
	return SkillDiagnostic{
		Type:    SkillDiagnosticWarning,
		Code:    code,
		Message: err.Error(),
		Path:    path,
	}
}

func cloneSkill(skill *Skill) *Skill {
	if skill == nil {
		return nil
	}
	cloned := *skill
	cloned.metadata = cloneMetadata(skill.metadata)
	return &cloned
}

func cloneMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return nil
	}
	cloned := make(map[string]any, len(metadata))
	for key, value := range metadata {
		cloned[key] = cloneMetadataValue(value)
	}
	return cloned
}

func cloneMetadataValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return cloneMetadata(value)
	case map[any]any:
		cloned := make(map[any]any, len(value))
		for key, item := range value {
			cloned[cloneMetadataValue(key)] = cloneMetadataValue(item)
		}
		return cloned
	case []any:
		cloned := make([]any, len(value))
		for index, item := range value {
			cloned[index] = cloneMetadataValue(item)
		}
		return cloned
	default:
		return value
	}
}

func setOptionalMetadata[T comparable](metadata map[string]any, key string, value T) {
	var zero T
	if value == zero {
		delete(metadata, key)
		return
	}
	metadata[key] = value
}

func cloneDiagnostic(diagnostic SkillDiagnostic) SkillDiagnostic {
	if diagnostic.Collision != nil {
		collision := *diagnostic.Collision
		diagnostic.Collision = &collision
	}
	return diagnostic
}

func utf16Length(value string) int {
	return len(utf16.Encode([]rune(value)))
}

func (s *Store) bindFileRoot(current os.FileInfo) error {
	if s.fileRoot != nil {
		bound, err := s.fileRoot.Stat(".")
		if err == nil && os.SameFile(current, bound) {
			return nil
		}
	}

	pathInfo, err := os.Lstat(s.dir)
	if err != nil {
		s.closeFileRoot()
		return err
	}
	if s.rootBound && pathInfo.Mode()&os.ModeSymlink != 0 {
		s.closeFileRoot()
		return fmt.Errorf("%w: root path changed to a symbolic link", ErrSkillPathOutsideRoot)
	}

	root, err := os.OpenRoot(s.dir)
	if err != nil {
		s.closeFileRoot()
		return err
	}
	bound, err := root.Stat(".")
	if err != nil {
		_ = root.Close()
		s.closeFileRoot()
		return err
	}
	if !os.SameFile(current, bound) {
		_ = root.Close()
		s.closeFileRoot()
		return ErrSkillChanged
	}

	s.closeFileRoot()
	s.fileRoot = root
	s.rootBound = true
	return nil
}

func (s *Store) rootMatchesCurrent() bool {
	if s.fileRoot == nil {
		return false
	}
	current, err := os.Stat(s.dir)
	if err != nil {
		return false
	}
	bound, err := s.fileRoot.Stat(".")
	return err == nil && os.SameFile(current, bound)
}

func (s *Store) closeFileRoot() {
	if s.fileRoot == nil {
		return
	}
	_ = s.fileRoot.Close()
	s.fileRoot = nil
}

func nearestExistingDir(path string) string {
	path = filepath.Clean(path)
	for {
		info, err := os.Stat(path)
		if err == nil && info.IsDir() {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return path
		}
		path = parent
	}
}

func (s *Store) syncWatchDirs(desired map[string]struct{}) {
	if s.watcher == nil {
		return
	}

	current := make(map[string]struct{})
	for _, path := range s.watcher.WatchList() {
		current[path] = struct{}{}
	}
	for path := range current {
		if _, ok := desired[path]; ok {
			continue
		}
		if err := s.watcher.Remove(path); err != nil {
			logutil.Warn("skill: cannot remove watch", zap.String("dir", path), zap.Error(err))
		}
	}

	paths := make([]string, 0, len(desired))
	for path := range desired {
		if _, ok := current[path]; !ok {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := s.watcher.Add(path); err != nil {
			logutil.Warn("skill: cannot watch dir", zap.String("dir", path), zap.Error(err))
		}
	}
}

func (s *Store) watchLoop() {
	for {
		select {
		case event, ok := <-s.watcher.Events:
			if !ok {
				return
			}
			if (event.Has(fsnotify.Create) || event.Has(fsnotify.Write) || event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename)) && s.pathAffectsRoot(event.Name) {
				logutil.Infof("skill: path changed, reloading %s", event.Name)
				if err := s.loadAll(); err != nil {
					logutil.Error("skill: reload failed", zap.Error(err))
				}
			}
		case err, ok := <-s.watcher.Errors:
			if !ok {
				return
			}
			logutil.Error("skill: watcher error", zap.Error(err))
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				if reloadErr := s.loadAll(); reloadErr != nil {
					logutil.Error("skill: overflow reload failed", zap.Error(reloadErr))
				}
			}
		}
	}
}

func (s *Store) pathAffectsRoot(path string) bool {
	path = filepath.Clean(path)
	root := filepath.Clean(s.dir)
	separator := string(filepath.Separator)
	return path == root || strings.HasPrefix(path, root+separator) || strings.HasPrefix(root, path+separator)
}
