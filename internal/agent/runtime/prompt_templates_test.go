package runtime

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadPromptTemplatesReadsMarkdownFilesNonRecursively(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, "a", "nested"))
	mustMkdirAll(t, filepath.Join(root, "b"))
	mustWriteFile(t, filepath.Join(root, "a", "one.md"), "---\ndescription: One template\n---\nHello $1")
	mustWriteFile(t, filepath.Join(root, "a", "nested", "ignored.md"), "Ignored")
	mustWriteFile(t, filepath.Join(root, "a", "ignored.txt"), "Ignored")
	mustWriteFile(t, filepath.Join(root, "b", "two.md"), "First line description\nBody")

	result := LoadPromptTemplates(filepath.Join(root, "a"), filepath.Join(root, "b"))

	want := []PromptTemplate{
		{Name: "one", Description: "One template", Content: "Hello $1"},
		{Name: "two", Description: "First line description", Content: "First line description\nBody"},
	}
	if !reflect.DeepEqual(result.Templates, want) {
		t.Fatalf("Templates = %#v, want %#v", result.Templates, want)
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("Diagnostics = %#v, want none", result.Diagnostics)
	}
}

func TestLoadPromptTemplatesUsesSourcePathNameForSymlinkedFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "target.md")
	link := filepath.Join(root, "link.md")
	mustWriteFile(t, target, "---\ndescription: Target\n---\nTarget body")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	result := LoadPromptTemplates(target, link)

	want := []PromptTemplate{
		{Name: "target", Description: "Target", Content: "Target body"},
		{Name: "link", Description: "Target", Content: "Target body"},
	}
	if !reflect.DeepEqual(result.Templates, want) {
		t.Fatalf("Templates = %#v, want %#v", result.Templates, want)
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("Diagnostics = %#v, want none", result.Diagnostics)
	}
}

func TestLoadPromptTemplatesReportsInvalidFrontmatterAndSkipsMissingPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	broken := filepath.Join(root, "broken.md")
	mustWriteFile(t, broken, "---\ndescription: [unterminated\n---\nBody")

	result := LoadPromptTemplates(filepath.Join(root, "missing.md"), broken)

	if len(result.Templates) != 0 {
		t.Fatalf("Templates = %#v, want none", result.Templates)
	}
	if len(result.Diagnostics) != 1 {
		t.Fatalf("Diagnostics = %#v, want one", result.Diagnostics)
	}
	diagnostic := result.Diagnostics[0]
	if diagnostic.Type != PromptTemplateDiagnosticWarning || diagnostic.Code != PromptTemplateDiagnosticParseFailed {
		t.Fatalf("Diagnostic = %#v", diagnostic)
	}
	if diagnostic.Path != broken {
		t.Fatalf("Path = %q, want %q", diagnostic.Path, broken)
	}
	if diagnostic.Message == "" {
		t.Fatal("Message is empty")
	}
}

func TestLoadPromptTemplatesReportsFileInfoFailure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	parent := filepath.Join(root, "file")
	mustWriteFile(t, parent, "content")
	path := filepath.Join(parent, "child.md")

	result := LoadPromptTemplates(path)

	if len(result.Diagnostics) != 1 {
		t.Fatalf("Diagnostics = %#v, want one", result.Diagnostics)
	}
	diagnostic := result.Diagnostics[0]
	if diagnostic.Code != PromptTemplateDiagnosticFileInfoFailed || diagnostic.Path != path {
		t.Fatalf("Diagnostic = %#v", diagnostic)
	}
}

func TestLoadPromptTemplatesNormalizesNewlinesAndTruncatesFallbackDescription(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "unicode.md")
	firstLine := strings.Repeat("界", 61)
	mustWriteFile(t, path, "---\r\nlabel: ignored\r\n---\r\n\r\n"+firstLine+"\r\nBody\r")

	result := LoadPromptTemplates(path)

	want := PromptTemplate{
		Name:        "unicode",
		Description: strings.Repeat("界", 60) + "...",
		Content:     firstLine + "\nBody",
	}
	if !reflect.DeepEqual(result.Templates, []PromptTemplate{want}) {
		t.Fatalf("Templates = %#v, want %#v", result.Templates, []PromptTemplate{want})
	}
}

func TestLoadPromptTemplatesTruncatesBeforeSplitUTF16Pair(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "emoji.md")
	firstLine := strings.Repeat("a", 59) + "🎉"
	mustWriteFile(t, path, firstLine)

	result := LoadPromptTemplates(path)

	want := PromptTemplate{
		Name:        "emoji",
		Description: strings.Repeat("a", 59) + "...",
		Content:     firstLine,
	}
	if !reflect.DeepEqual(result.Templates, []PromptTemplate{want}) {
		t.Fatalf("Templates = %#v, want %#v", result.Templates, []PromptTemplate{want})
	}
}

func TestLoadPromptTemplatesAcceptsScalarFrontmatterAndPreservesUnclosedFrontmatter(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	scalar := filepath.Join(root, "scalar.md")
	unclosed := filepath.Join(root, "unclosed.md")
	mustWriteFile(t, scalar, "---\nscalar metadata\n---\nScalar body")
	mustWriteFile(t, unclosed, "---\ndescription: Unclosed\nBody")

	result := LoadPromptTemplates(scalar, unclosed)

	want := []PromptTemplate{
		{Name: "scalar", Description: "Scalar body", Content: "Scalar body"},
		{Name: "unclosed", Description: "---", Content: "---\ndescription: Unclosed\nBody"},
	}
	if !reflect.DeepEqual(result.Templates, want) {
		t.Fatalf("Templates = %#v, want %#v", result.Templates, want)
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("Diagnostics = %#v, want none", result.Diagnostics)
	}
}

func TestLoadSourcedPromptTemplatesPreservesSourcesOnTemplatesAndDiagnostics(t *testing.T) {
	t.Parallel()

	type source struct{ Scope string }
	root := t.TempDir()
	valid := filepath.Join(root, "valid.md")
	broken := filepath.Join(root, "broken.md")
	mustWriteFile(t, valid, "Valid body")
	mustWriteFile(t, broken, "---\ndescription: [unterminated\n---\nBody")

	result := LoadSourcedPromptTemplates([]SourcedPromptTemplatePath[source]{
		{Path: valid, Source: source{Scope: "project"}},
		{Path: broken, Source: source{Scope: "user"}},
	})

	wantTemplates := []SourcedPromptTemplate[source]{
		{Template: PromptTemplate{Name: "valid", Description: "Valid body", Content: "Valid body"}, Source: source{Scope: "project"}},
	}
	if !reflect.DeepEqual(result.Templates, wantTemplates) {
		t.Fatalf("Templates = %#v, want %#v", result.Templates, wantTemplates)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Source.Scope != "user" {
		t.Fatalf("Diagnostics = %#v", result.Diagnostics)
	}
	if result.Diagnostics[0].Path != broken {
		t.Fatalf("diagnostic path = %q, want %q", result.Diagnostics[0].Path, broken)
	}
}

func TestLoadSourcedPromptTemplatesWithMapperTransformsSuccessfulTemplates(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "review.md")
	mustWriteFile(t, path, "Review body")

	result := LoadSourcedPromptTemplatesWithMapper(
		[]SourcedPromptTemplatePath[string]{{Path: path, Source: "project"}},
		func(template PromptTemplate, source string) PromptTemplate {
			template.Description = source + ": " + template.Description
			return template
		},
	)

	want := PromptTemplate{Name: "review", Description: "project: Review body", Content: "Review body"}
	if len(result.Templates) != 1 || result.Templates[0].Template != want {
		t.Fatalf("Templates = %#v, want %#v", result.Templates, want)
	}
}

func TestParseCommandArgsMatchesPromptTemplateContract(t *testing.T) {
	t.Parallel()

	args := ParseCommandArgs("plain \"two words\" 'three words' escaped\\ value line\nbreak adjacent\"quoted\"")
	want := []string{"plain", "two words", "three words", `escaped\`, "value", "line\nbreak", "adjacentquoted"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}

	if got := ParseCommandArgs(`"unterminated`); !reflect.DeepEqual(got, []string{"unterminated"}) {
		t.Fatalf("unterminated args = %#v", got)
	}
	if got := ParseCommandArgs(`"" ''`); len(got) != 0 {
		t.Fatalf("empty quoted args = %#v", got)
	}
}

func TestFormatPromptTemplateInvocationSubstitutesArguments(t *testing.T) {
	t.Parallel()

	template := PromptTemplate{
		Name:    "review",
		Content: "$1|$2|${@:2}|${@:2:1}|${@:0:2}|$ARGUMENTS|$@|$9",
	}
	got := FormatPromptTemplateInvocation(template, []string{"hello world", "test", "tail"})
	want := "hello world|test|test tail|test|hello world test|hello world test tail|hello world test tail|"
	if got != want {
		t.Fatalf("FormatPromptTemplateInvocation() = %q, want %q", got, want)
	}
}

func TestExpandPromptTemplateCommandUsesFirstExactMatch(t *testing.T) {
	t.Parallel()

	templates := []PromptTemplate{
		{Name: "review", Content: "Review $1 with ${@:2}"},
		{Name: "review", Content: "ignored"},
	}
	got := ExpandPromptTemplateCommand("/review target \"extra context\"\nnext", templates)
	if want := "Review target with extra context\nnext"; got != want {
		t.Fatalf("ExpandPromptTemplateCommand() = %q, want %q", got, want)
	}
	for _, input := range []string{"plain text", "/Review target", "/missing target", "/"} {
		if got := ExpandPromptTemplateCommand(input, templates); got != input {
			t.Fatalf("ExpandPromptTemplateCommand(%q) = %q", input, got)
		}
	}
	if got, ok := ResolvePromptTemplateCommand("/review target", templates); !ok || got != "Review target with " {
		t.Fatalf("ResolvePromptTemplateCommand() = %q, %v", got, ok)
	}
}

func TestSubstitutePromptTemplateArgsPreservesReplacementOrder(t *testing.T) {
	t.Parallel()

	got := SubstitutePromptTemplateArgs(`$0|$01|$10|${@:2:0}|${1}|${1:-fallback}|\$1|$2`, []string{"$ARGUMENTS", "x"})
	want := `|$ARGUMENTS x|||${1}|${1:-fallback}|\$ARGUMENTS x|x`
	if got != want {
		t.Fatalf("SubstitutePromptTemplateArgs() = %q, want %q", got, want)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}
