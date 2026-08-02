package runtime

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"go.abhg.dev/goldmark/frontmatter"
	"gopkg.in/yaml.v3"
)

const PromptTemplateDiagnosticWarning = "warning"

// PromptTemplateDiagnosticCode identifies a stable template-loading failure category.
type PromptTemplateDiagnosticCode string

const (
	PromptTemplateDiagnosticFileInfoFailed PromptTemplateDiagnosticCode = "file_info_failed"
	PromptTemplateDiagnosticListFailed     PromptTemplateDiagnosticCode = "list_failed"
	PromptTemplateDiagnosticReadFailed     PromptTemplateDiagnosticCode = "read_failed"
	PromptTemplateDiagnosticParseFailed    PromptTemplateDiagnosticCode = "parse_failed"
)

// PromptTemplate is a Markdown prompt available for explicit invocation.
type PromptTemplate struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Content     string `json:"content"`
}

// PromptTemplateDiagnostic describes a warning produced while loading templates.
type PromptTemplateDiagnostic struct {
	Type    string                       `json:"type"`
	Code    PromptTemplateDiagnosticCode `json:"code"`
	Message string                       `json:"message"`
	Path    string                       `json:"path"`
}

// PromptTemplateLoadResult contains every successfully loaded template and warning.
type PromptTemplateLoadResult struct {
	Templates   []PromptTemplate           `json:"templates"`
	Diagnostics []PromptTemplateDiagnostic `json:"diagnostics"`
}

// SourcedPromptTemplatePath associates an input path with caller-owned provenance.
type SourcedPromptTemplatePath[S any] struct {
	Path   string `json:"path"`
	Source S      `json:"source"`
}

// SourcedPromptTemplate preserves provenance for a loaded template.
type SourcedPromptTemplate[S any] struct {
	Template PromptTemplate `json:"template"`
	Source   S              `json:"source"`
}

// SourcedPromptTemplateDiagnostic preserves provenance for a loading warning.
type SourcedPromptTemplateDiagnostic[S any] struct {
	PromptTemplateDiagnostic
	Source S `json:"source"`
}

// SourcedPromptTemplateLoadResult contains source-tagged templates and warnings.
type SourcedPromptTemplateLoadResult[S any] struct {
	Templates   []SourcedPromptTemplate[S]           `json:"templates"`
	Diagnostics []SourcedPromptTemplateDiagnostic[S] `json:"diagnostics"`
}

// LoadPromptTemplates reads explicit Markdown files and direct Markdown children of directories.
func LoadPromptTemplates(paths ...string) PromptTemplateLoadResult {
	result := PromptTemplateLoadResult{
		Templates:   make([]PromptTemplate, 0),
		Diagnostics: make([]PromptTemplateDiagnostic, 0),
	}
	for _, path := range paths {
		info, err := inspectPath(path)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				result.Diagnostics = append(result.Diagnostics, promptTemplateDiagnostic(PromptTemplateDiagnosticFileInfoFailed, path, err))
			}
			continue
		}

		kind, ok := resolveKind(info, &result.Diagnostics)
		switch {
		case !ok:
			continue
		case kind == pathDirectory:
			loaded := loadPromptTemplateDirectory(info.path)
			result.Templates = append(result.Templates, loaded.Templates...)
			result.Diagnostics = append(result.Diagnostics, loaded.Diagnostics...)
		case kind == pathFile && strings.HasSuffix(info.name, ".md"):
			template, diagnostics := loadPromptTemplateFile(info.path)
			if template != nil {
				result.Templates = append(result.Templates, *template)
			}
			result.Diagnostics = append(result.Diagnostics, diagnostics...)
		}
	}
	return result
}

// LoadSourcedPromptTemplates loads templates while preserving each input's provenance.
func LoadSourcedPromptTemplates[S any](inputs []SourcedPromptTemplatePath[S]) SourcedPromptTemplateLoadResult[S] {
	return loadSourcedPromptTemplates(inputs, nil)
}

// LoadSourcedPromptTemplatesWithMapper applies mapper to each successful template before attaching provenance.
func LoadSourcedPromptTemplatesWithMapper[S any](
	inputs []SourcedPromptTemplatePath[S],
	mapper func(PromptTemplate, S) PromptTemplate,
) SourcedPromptTemplateLoadResult[S] {
	return loadSourcedPromptTemplates(inputs, mapper)
}

func loadSourcedPromptTemplates[S any](inputs []SourcedPromptTemplatePath[S], mapper func(PromptTemplate, S) PromptTemplate) SourcedPromptTemplateLoadResult[S] {
	result := SourcedPromptTemplateLoadResult[S]{
		Templates:   make([]SourcedPromptTemplate[S], 0),
		Diagnostics: make([]SourcedPromptTemplateDiagnostic[S], 0),
	}
	for _, input := range inputs {
		loaded := LoadPromptTemplates(input.Path)
		for _, template := range loaded.Templates {
			if mapper != nil {
				template = mapper(template, input.Source)
			}
			result.Templates = append(result.Templates, SourcedPromptTemplate[S]{
				Template: template,
				Source:   input.Source,
			})
		}
		for _, item := range loaded.Diagnostics {
			result.Diagnostics = append(result.Diagnostics, SourcedPromptTemplateDiagnostic[S]{
				PromptTemplateDiagnostic: item,
				Source:                   input.Source,
			})
		}
	}
	return result
}

var (
	commandArgumentPattern  = regexp.MustCompile(`(?:[^ \t'"]+|'[^']*'|"[^"]*"|'[^']*$|"[^"]*$)+`)
	commandFragmentPattern  = regexp.MustCompile(`[^'"]+|'[^']*'|"[^"]*"|'[^']*$|"[^"]*$`)
	promptInvocationPattern = regexp.MustCompile(`^/([^\s]+)(?:\s+([\s\S]*))?$`)
)

// ParseCommandArgs splits command text on spaces and tabs while preserving quoted groups.
func ParseCommandArgs(input string) []string {
	tokens := commandArgumentPattern.FindAllString(input, -1)
	args := make([]string, 0, len(tokens))
	for _, token := range tokens {
		var argument strings.Builder
		for _, fragment := range commandFragmentPattern.FindAllString(token, -1) {
			if len(fragment) > 0 && (fragment[0] == '\'' || fragment[0] == '"') {
				quote := fragment[0]
				fragment = fragment[1:]
				if len(fragment) > 0 && fragment[len(fragment)-1] == quote {
					fragment = fragment[:len(fragment)-1]
				}
			}
			argument.WriteString(fragment)
		}
		if argument.Len() > 0 {
			args = append(args, argument.String())
		}
	}
	return args
}

var (
	positionalArgumentPattern = regexp.MustCompile(`\$([0-9]+)`)
	slicedArgumentsPattern    = regexp.MustCompile(`\$\{@:([0-9]+)(:([0-9]+))?\}`)
)

// SubstitutePromptTemplateArgs expands positional and aggregate argument placeholders.
func SubstitutePromptTemplateArgs(content string, args []string) string {
	result := positionalArgumentPattern.ReplaceAllStringFunc(content, func(placeholder string) string {
		matches := positionalArgumentPattern.FindStringSubmatch(placeholder)
		position, err := strconv.ParseUint(matches[1], 10, 64)
		if err != nil || position == 0 || position > uint64(len(args)) {
			return ""
		}
		return args[position-1]
	})

	result = slicedArgumentsPattern.ReplaceAllStringFunc(result, func(placeholder string) string {
		matches := slicedArgumentsPattern.FindStringSubmatch(placeholder)
		position, err := strconv.ParseUint(matches[1], 10, 64)
		if err != nil {
			return ""
		}
		start := uint64(0)
		if position > 0 {
			start = position - 1
		}
		if start >= uint64(len(args)) {
			return ""
		}

		end := uint64(len(args))
		if matches[3] != "" {
			length, parseErr := strconv.ParseUint(matches[3], 10, 64)
			if parseErr != nil {
				return ""
			}
			if length < end-start {
				end = start + length
			}
		}
		return strings.Join(args[int(start):int(end)], " ")
	})

	allArgs := strings.Join(args, " ")
	result = strings.ReplaceAll(result, "$ARGUMENTS", allArgs)
	return strings.ReplaceAll(result, "$@", allArgs)
}

// FormatPromptTemplateInvocation expands arguments in a template's content.
func FormatPromptTemplateInvocation(template PromptTemplate, args []string) string {
	return SubstitutePromptTemplateArgs(template.Content, args)
}

// ExpandPromptTemplateCommand expands a matching slash command and preserves other input.
func ExpandPromptTemplateCommand(input string, templates []PromptTemplate) string {
	expanded, ok := ResolvePromptTemplateCommand(input, templates)
	if !ok {
		return input
	}
	return expanded
}

// ResolvePromptTemplateCommand expands a matching slash command and reports whether a template matched.
func ResolvePromptTemplateCommand(input string, templates []PromptTemplate) (string, bool) {
	if !strings.HasPrefix(input, "/") {
		return "", false
	}
	matches := promptInvocationPattern.FindStringSubmatch(input)
	if len(matches) == 0 {
		return "", false
	}
	for _, template := range templates {
		if template.Name == matches[1] {
			return FormatPromptTemplateInvocation(template, ParseCommandArgs(matches[2])), true
		}
	}
	return "", false
}

func clonePromptTemplates(templates []PromptTemplate) []PromptTemplate {
	if templates == nil {
		return nil
	}
	return slices.Clone(templates)
}

type pathKind uint8

const (
	pathOther pathKind = iota
	pathFile
	pathDirectory
	pathSymlink
)

type pathInfo struct {
	name string
	path string
	kind pathKind
}

func loadPromptTemplateDirectory(dir string) PromptTemplateLoadResult {
	result := PromptTemplateLoadResult{
		Templates:   make([]PromptTemplate, 0),
		Diagnostics: make([]PromptTemplateDiagnostic, 0),
	}
	infos, err := listDirectory(dir)
	if err != nil {
		result.Diagnostics = append(result.Diagnostics, promptTemplateDiagnostic(PromptTemplateDiagnosticListFailed, dir, err))
		return result
	}

	for _, info := range infos {
		kind, ok := resolveKind(info, &result.Diagnostics)
		if !ok || kind != pathFile || !strings.HasSuffix(info.name, ".md") {
			continue
		}
		template, diagnostics := loadPromptTemplateFile(info.path)
		if template != nil {
			result.Templates = append(result.Templates, *template)
		}
		result.Diagnostics = append(result.Diagnostics, diagnostics...)
	}
	return result
}

func listDirectory(dir string) ([]pathInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	infos := make([]pathInfo, 0, len(entries))
	for _, entry := range entries {
		info, inspectErr := inspectPath(filepath.Join(dir, entry.Name()))
		if inspectErr != nil {
			return nil, inspectErr
		}
		infos = append(infos, info)
	}
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].name < infos[j].name
	})
	return infos, nil
}

func loadPromptTemplateFile(path string) (*PromptTemplate, []PromptTemplateDiagnostic) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, []PromptTemplateDiagnostic{promptTemplateDiagnostic(PromptTemplateDiagnosticReadFailed, path, err)}
	}

	description, body, err := parseDocument(content)
	if err != nil {
		return nil, []PromptTemplateDiagnostic{promptTemplateDiagnostic(PromptTemplateDiagnosticParseFailed, path, err)}
	}
	if description == "" {
		description = fallbackDescription(body)
	}

	return &PromptTemplate{
		Name:        strings.TrimSuffix(filepath.Base(path), ".md"),
		Description: description,
		Content:     body,
	}, nil
}

func parseDocument(content []byte) (description, body string, err error) {
	normalized := strings.ReplaceAll(string(content), "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")

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
	markdown.Parser().Parse(text.NewReader([]byte(normalized)), parser.WithContext(ctx))
	metadata := frontmatter.Get(ctx)
	if metadata == nil {
		return "", normalized, nil
	}

	var decoded any
	decodeErr := metadata.Decode(&decoded)
	documentBody, ok := promptTemplateBody([]byte(normalized), rawFrontmatter)
	if !ok {
		return "", normalized, nil
	}
	if decodeErr != nil {
		return "", "", decodeErr
	}
	if fields, ok := decoded.(map[string]any); ok {
		description, _ = fields["description"].(string)
	}
	return description, strings.TrimSpace(string(documentBody)), nil
}

func promptTemplateBody(document, rawFrontmatter []byte) ([]byte, bool) {
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

func fallbackDescription(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		units := 0
		for index, char := range line {
			width := utf16.RuneLen(char)
			if units+width > 60 {
				return line[:index] + "..."
			}
			units += width
		}
		return line
	}
	return ""
}

func inspectPath(path string) (pathInfo, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return pathInfo{}, err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return pathInfo{}, err
	}

	kind := pathOther
	switch {
	case info.Mode().IsRegular():
		kind = pathFile
	case info.IsDir():
		kind = pathDirectory
	case info.Mode()&os.ModeSymlink != 0:
		kind = pathSymlink
	}
	return pathInfo{name: filepath.Base(absolute), path: absolute, kind: kind}, nil
}

func resolveKind(info pathInfo, diagnostics *[]PromptTemplateDiagnostic) (pathKind, bool) {
	if info.kind == pathFile || info.kind == pathDirectory {
		return info.kind, true
	}
	if info.kind != pathSymlink {
		return pathOther, false
	}

	canonical, err := filepath.EvalSymlinks(info.path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			*diagnostics = append(*diagnostics, promptTemplateDiagnostic(PromptTemplateDiagnosticFileInfoFailed, info.path, err))
		}
		return pathOther, false
	}
	target, err := inspectPath(canonical)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			*diagnostics = append(*diagnostics, promptTemplateDiagnostic(PromptTemplateDiagnosticFileInfoFailed, info.path, err))
		}
		return pathOther, false
	}
	if target.kind == pathFile || target.kind == pathDirectory {
		return target.kind, true
	}
	return pathOther, false
}

func promptTemplateDiagnostic(code PromptTemplateDiagnosticCode, path string, err error) PromptTemplateDiagnostic {
	return PromptTemplateDiagnostic{
		Type:    PromptTemplateDiagnosticWarning,
		Code:    code,
		Message: err.Error(),
		Path:    path,
	}
}
