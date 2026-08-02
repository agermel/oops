package skills

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestStoreLoadsRootAndNestedSkills(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, filepath.Join(dir, "root.md"), `---
name: root-entry
description: Root skill
enabled: false
---
Root content.
`)
	writeSkillFile(t, filepath.Join(dir, "groups", "example", "SKILL.md"), "---\r\nname: example\r\ndescription: Example skill\r\ndisable-model-invocation: true\r\nicon: Bug\r\nlabel: Test\r\ncolor: custom\r\n---\r\nUse this skill.\r\n")
	writeSkillFile(t, filepath.Join(dir, "groups", "ignored.md"), "---\nname: ignored\ndescription: Ignored\n---\nIgnored.\n")
	writeSkillFile(t, filepath.Join(dir, "bundle", "SKILL.md"), "---\nname: bundle\ndescription: Bundle skill\n---\nBundle.\n")
	writeSkillFile(t, filepath.Join(dir, "bundle", "child", "SKILL.md"), "---\nname: child\ndescription: Child skill\n---\nChild.\n")
	writeSkillFile(t, filepath.Join(dir, ".hidden", "hidden", "SKILL.md"), "---\nname: hidden\ndescription: Hidden\n---\nHidden.\n")
	writeSkillFile(t, filepath.Join(dir, "node_modules", "dependency", "SKILL.md"), "---\nname: dependency\ndescription: Dependency\n---\nDependency.\n")

	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	listed := store.List()
	names := make([]string, len(listed))
	for i, skill := range listed {
		names[i] = skill.Name
	}
	if want := []string{"bundle", "example", "root-entry"}; !slices.Equal(names, want) {
		t.Fatalf("List() names = %v, want %v", names, want)
	}

	example, ok := store.Get("example")
	if !ok {
		t.Fatal("expected example skill")
	}
	if example.Content != "Use this skill." {
		t.Fatalf("Content = %q", example.Content)
	}
	if example.FilePath != filepath.Join(dir, "groups", "example", "SKILL.md") {
		t.Fatalf("FilePath = %q", example.FilePath)
	}
	if !example.DisableModelInvocation || example.Icon != "Bug" || example.Label != "Test" || example.Color != "custom" {
		t.Fatalf("project metadata was not preserved: %#v", example)
	}
	if !example.Enabled {
		t.Fatal("Enabled should default to true")
	}
	if _, ok := store.Get("child"); ok {
		t.Fatal("a skill directory must stop recursive discovery")
	}
	if _, ok := store.Get("ignored"); ok {
		t.Fatal("nested loose Markdown files must be ignored")
	}
}

func TestStoreReturnsCopiesAndUpdatesAtomically(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, filepath.Join(dir, "example", "SKILL.md"), "---\nname: example\ndescription: Original\n---\nOriginal body.\n")
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	fromGet, _ := store.Get("example")
	fromGet.Description = "mutated get"
	fromList := store.List()
	fromList[0].Content = "mutated list"
	fromEnabled := store.Enabled()
	fromEnabled[0].Icon = "mutated enabled"

	stored, _ := store.Get("example")
	if stored.Description != "Original" || stored.Content != "Original body." || stored.Icon != "" {
		t.Fatalf("read methods exposed store-owned data: %#v", stored)
	}

	updated := *stored
	updated.Description = "Updated"
	updated.Content = "Updated body."
	updated.Icon = "Check"
	if !store.Update(&updated) {
		t.Fatal("Update() rejected an existing skill")
	}
	updated.Description = "mutated caller"

	stored, _ = store.Get("example")
	if stored.Description != "Updated" || stored.Content != "Updated body." || stored.Icon != "Check" {
		t.Fatalf("Update() did not replace the complete skill: %#v", stored)
	}
	if store.Update(nil) {
		t.Fatal("Update(nil) succeeded")
	}
	if store.Update(&Skill{Name: "missing"}) {
		t.Fatal("Update() inserted a missing skill")
	}
}

func TestStoreRenderAvailableEscapesAndFilters(t *testing.T) {
	store := &Store{skills: map[string]*Skill{
		`z&"`: {
			Name:        `z&"`,
			Description: `Use <probe> & report "why".`,
			FilePath:    `/tmp/z&"/SKILL.md`,
			Enabled:     true,
		},
		"disabled": {
			Name:        "disabled",
			Description: "Disabled",
			FilePath:    "/tmp/disabled/SKILL.md",
			Enabled:     false,
		},
		"hidden": {
			Name:                   "hidden",
			Description:            "Explicit only",
			FilePath:               "/tmp/hidden/SKILL.md",
			DisableModelInvocation: true,
			Enabled:                true,
		},
	}}

	got := store.RenderAvailable()
	for _, want := range []string{
		`<name>z&amp;&#34;</name>`,
		`<description>Use &lt;probe&gt; &amp; report &#34;why&#34;.</description>`,
		`<location>/tmp/z&amp;&#34;/SKILL.md</location>`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("RenderAvailable() missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "disabled") || strings.Contains(got, "hidden") {
		t.Fatalf("RenderAvailable() exposed filtered skills:\n%s", got)
	}
	if skill, ok := store.Get("hidden"); !ok || skill.Description != "Explicit only" {
		t.Fatal("explicit lookup must retain hidden skills")
	}
}

func TestStoreAggregatesDiagnostics(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, filepath.Join(dir, "bad-yaml", "SKILL.md"), "---\nname: [\n---\nBody.\n")
	writeSkillFile(t, filepath.Join(dir, "missing-description", "SKILL.md"), "---\nname: missing-description\n---\nBody.\n")
	writeSkillFile(t, filepath.Join(dir, "valid-dir", "SKILL.md"), "---\nname: Bad--Name\ndescription: Present\n---\nBody.\n")
	writeSkillFile(t, filepath.Join(dir, "long-description", "SKILL.md"), "---\nname: long-description\ndescription: "+strings.Repeat("x", maxDescriptionLength+1)+"\n---\nBody.\n")
	if err := os.Symlink("missing-target", filepath.Join(dir, "broken-link")); err != nil {
		t.Fatalf("create broken symlink: %v", err)
	}

	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	diagnostics := store.Diagnostics()
	assertDiagnosticCode(t, diagnostics, SkillDiagnosticParseFailed)
	assertDiagnosticCode(t, diagnostics, SkillDiagnosticInvalidMetadata)
	assertDiagnosticCode(t, diagnostics, SkillDiagnosticFileInfoFailed)
	if _, ok := store.Get("missing-description"); ok {
		t.Fatal("skill without a description was loaded")
	}
	if _, ok := store.Get("Bad--Name"); !ok {
		t.Fatal("skill with warned name metadata should remain explicitly available")
	}
	if _, ok := store.Get("long-description"); !ok {
		t.Fatal("skill with a long description should remain explicitly available")
	}

	diagnostics[0].Message = "mutated"
	if store.Diagnostics()[0].Message == "mutated" {
		t.Fatal("Diagnostics() exposed store-owned data")
	}
}

func TestStoreKeepsFirstSkillNameCollision(t *testing.T) {
	dir := t.TempDir()
	winnerPath := filepath.Join(dir, "a", "shared", "SKILL.md")
	loserPath := filepath.Join(dir, "b", "shared", "SKILL.md")
	writeSkillFile(t, winnerPath, "---\nname: shared\ndescription: First\n---\nFirst body.\n")
	writeSkillFile(t, loserPath, "---\nname: shared\ndescription: Second\n---\nSecond body.\n")
	if err := os.MkdirAll(filepath.Join(dir, "c"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "a", "shared"), filepath.Join(dir, "c", "shared")); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	shared, ok := store.Get("shared")
	if !ok || shared.Description != "First" || shared.FilePath != winnerPath {
		t.Fatalf("shared skill = %#v, want first path %q", shared, winnerPath)
	}

	var collisions []SkillDiagnostic
	for _, diagnostic := range store.Diagnostics() {
		if diagnostic.Code == SkillDiagnosticNameCollision {
			collisions = append(collisions, diagnostic)
		}
	}
	if len(collisions) != 1 {
		t.Fatalf("collision diagnostics = %#v, want one", collisions)
	}
	collision := collisions[0]
	if collision.Type != SkillDiagnosticCollision || collision.Path != loserPath || collision.Collision == nil {
		t.Fatalf("collision diagnostic = %#v", collision)
	}
	if collision.Collision.ResourceType != "skill" || collision.Collision.Name != "shared" || collision.Collision.WinnerPath != winnerPath || collision.Collision.LoserPath != loserPath {
		t.Fatalf("collision details = %#v", collision.Collision)
	}
	collision.Collision.WinnerPath = "mutated"
	for _, diagnostic := range store.Diagnostics() {
		if diagnostic.Code == SkillDiagnosticNameCollision && diagnostic.Collision.WinnerPath == "mutated" {
			t.Fatal("Diagnostics() exposed collision-owned data")
		}
	}
}

func TestStoreHonorsLayeredIgnoreFiles(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, filepath.Join(dir, ".gitignore"), "ignored-git/\ngroup/blocked/\nroot-ignored.md\nfile-boundary/SKILL.md\nwild/*\n!wild/keep/\n**/deep-ignored/\n")
	writeSkillFile(t, filepath.Join(dir, ".ignore"), "ignored-ignore/\n")
	writeSkillFile(t, filepath.Join(dir, ".fdignore"), "ignored-fd/\n")
	writeSkillFile(t, filepath.Join(dir, "group", ".gitignore"), "local/\n")
	writeSkillFile(t, filepath.Join(dir, "group", ".ignore"), "!blocked/\n")
	writeSkillFile(t, filepath.Join(dir, "root-ignored.md"), "---\nname: root-ignored\ndescription: root-ignored\n---\nBody.\n")
	writeSkillFile(t, filepath.Join(dir, "file-boundary", "SKILL.md"), "---\nname: file-boundary\ndescription: file-boundary\n---\nBody.\n")
	writeSkillFile(t, filepath.Join(dir, "file-boundary", "child", "SKILL.md"), "---\nname: child\ndescription: child\n---\nBody.\n")
	writeSkillFile(t, filepath.Join(dir, "wild", "drop", "SKILL.md"), "---\nname: drop\ndescription: drop\n---\nBody.\n")
	writeSkillFile(t, filepath.Join(dir, "wild", "keep", "SKILL.md"), "---\nname: keep\ndescription: keep\n---\nBody.\n")
	writeSkillFile(t, filepath.Join(dir, "somewhere", "deep-ignored", "SKILL.md"), "---\nname: deep-ignored\ndescription: deep-ignored\n---\nBody.\n")

	for path, name := range map[string]string{
		filepath.Join("ignored-git", "SKILL.md"):        "ignored-git",
		filepath.Join("ignored-ignore", "SKILL.md"):     "ignored-ignore",
		filepath.Join("ignored-fd", "SKILL.md"):         "ignored-fd",
		filepath.Join("group", "blocked", "SKILL.md"):   "blocked",
		filepath.Join("group", "local", "SKILL.md"):     "local",
		filepath.Join("outside", "local", "SKILL.md"):   "local",
		filepath.Join("outside", "visible", "SKILL.md"): "visible",
	} {
		writeSkillFile(t, filepath.Join(dir, path), "---\nname: "+name+"\ndescription: "+name+"\n---\nBody.\n")
	}

	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	listed := store.List()
	names := make([]string, len(listed))
	for index, skill := range listed {
		names[index] = skill.Name
	}
	if want := []string{"blocked", "child", "keep", "local", "visible"}; !slices.Equal(names, want) {
		t.Fatalf("List() names = %v, want %v", names, want)
	}

	writeSkillFile(t, filepath.Join(dir, "group", ".ignore"), "")
	waitForSkill(t, store, "blocked", func(_ *Skill, ok bool) bool { return !ok })
	writeSkillFile(t, filepath.Join(dir, "group", "blocked", "SKILL.md"), "---\nname: blocked\ndescription: Reloaded\n---\nReloaded body.\n")
	writeSkillFile(t, filepath.Join(dir, "group", ".ignore"), "!blocked/\n")
	waitForSkill(t, store, "blocked", func(skill *Skill, ok bool) bool {
		return ok && skill.Description == "Reloaded"
	})
}

func TestStoreHonorsIgnoreWildcardsEscapesAndTrailingSpaces(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, filepath.Join(dir, ".gitignore"), "skill-?/\n\\!literal/\n\\#literal/\ntrailing/   \nescaped\\ /\n")
	for path, name := range map[string]string{
		filepath.Join("skill-a", "SKILL.md"):  "skill-a",
		filepath.Join("skill-aa", "SKILL.md"): "skill-aa",
		filepath.Join("!literal", "SKILL.md"): "literal-bang",
		filepath.Join("#literal", "SKILL.md"): "literal-hash",
		filepath.Join("trailing", "SKILL.md"): "trailing",
		filepath.Join("escaped ", "SKILL.md"):  "escaped-space",
		filepath.Join("visible", "SKILL.md"):   "visible",
	} {
		writeSkillFile(t, filepath.Join(dir, path), "---\nname: "+name+"\ndescription: "+name+"\n---\nBody.\n")
	}

	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	listed := store.List()
	names := make([]string, len(listed))
	for index, skill := range listed {
		names[index] = skill.Name
	}
	if want := []string{"skill-aa", "visible"}; !slices.Equal(names, want) {
		t.Fatalf("List() names = %v, want %v", names, want)
	}
}

func TestStoreSavePreservesExtensionFrontmatter(t *testing.T) {
	dir := t.TempDir()
	skillPath := filepath.Join(dir, "example", "SKILL.md")
	writeSkillFile(t, skillPath, `---
name: example
description: Original
owner: runtime
options:
  retries: 3
  labels:
    - stable
    - local
---
Original body.
`)
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	updated, ok := store.Get("example")
	if !ok {
		t.Fatal("expected example skill")
	}
	updated.Description = "Updated"
	updated.Content = "Updated body."
	updated.Icon = "Check"
	if err := store.Save(updated); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatal(err)
	}
	fields, body, err := parseDocument(data)
	if err != nil {
		t.Fatalf("parseDocument() error = %v", err)
	}
	if fields["owner"] != "runtime" {
		t.Fatalf("owner = %#v", fields["owner"])
	}
	wantOptions := map[string]any{
		"retries": 3,
		"labels":  []any{"stable", "local"},
	}
	if !reflect.DeepEqual(fields["options"], wantOptions) {
		t.Fatalf("options = %#v, want %#v", fields["options"], wantOptions)
	}
	if fields["description"] != "Updated" || fields["icon"] != "Check" || body != "Updated body." {
		t.Fatalf("saved skill = fields %#v, body %q", fields, body)
	}
}

func TestStoreUsesJavaScriptStringLengthSemantics(t *testing.T) {
	name := strings.Repeat("a", maxNameLength-1) + "😀"
	diagnostics := validateSkillMetadata(&Skill{
		Name:        name,
		Description: strings.Repeat("x", maxDescriptionLength-1) + "😀",
	}, name, "/skills/long/SKILL.md")

	for _, want := range []string{
		"name exceeds 64 characters (65)",
		"description exceeds 1024 characters (1025)",
	} {
		found := false
		for _, diagnostic := range diagnostics {
			if diagnostic.Message == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("diagnostics = %#v, missing %q", diagnostics, want)
		}
	}

	diagnostics = validateSkillMetadata(&Skill{
		Name:        "valid",
		Description: strings.Repeat("界", maxDescriptionLength),
	}, "valid", "/skills/valid/SKILL.md")
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic.Message, "description exceeds") {
			t.Fatalf("BMP description was overcounted: %#v", diagnostics)
		}
	}
}

func TestStoreHotReloadsNestedSkills(t *testing.T) {
	dir := t.TempDir()
	skillPath := filepath.Join(dir, "groups", "example", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	writeSkillFile(t, skillPath, "---\nname: example\ndescription: First\n---\nFirst body.\n")
	waitForSkill(t, store, "example", func(skill *Skill, ok bool) bool {
		return ok && skill.Description == "First" && skill.Content == "First body."
	})

	writeSkillFile(t, skillPath, "---\nname: example\ndescription: Second\n---\nSecond body.\n")
	waitForSkill(t, store, "example", func(skill *Skill, ok bool) bool {
		return ok && skill.Description == "Second" && skill.Content == "Second body."
	})

	newPath := filepath.Join(dir, "later", "new-skill", "SKILL.md")
	writeSkillFile(t, newPath, "---\nname: new-skill\ndescription: Added later\n---\nLater.\n")
	waitForSkill(t, store, "new-skill", func(_ *Skill, ok bool) bool { return ok })

	if err := os.Remove(skillPath); err != nil {
		t.Fatal(err)
	}
	waitForSkill(t, store, "example", func(_ *Skill, ok bool) bool { return !ok })
}

func TestStoreLoadsWhenMissingRootIsCreated(t *testing.T) {
	root := filepath.Join(t.TempDir(), "later", "nested", "skills")
	store, err := NewStore(root)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()
	if len(store.List()) != 0 {
		t.Fatalf("missing root loaded skills: %#v", store.List())
	}

	writeSkillFile(t, filepath.Join(root, "first", "SKILL.md"), "---\nname: first\ndescription: First\n---\nFirst.\n")
	waitForSkill(t, store, "first", func(_ *Skill, ok bool) bool { return ok })

	writeSkillFile(t, filepath.Join(root, "added", "later", "SKILL.md"), "---\nname: later\ndescription: Later\n---\nLater.\n")
	waitForSkill(t, store, "later", func(_ *Skill, ok bool) bool { return ok })
}

func TestStoreReloadsAfterRootDeletionAndRecreation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	writeSkillFile(t, filepath.Join(root, "before", "SKILL.md"), "---\nname: before\ndescription: Before\n---\nBefore.\n")
	store, err := NewStore(root)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	waitForSkill(t, store, "before", func(_ *Skill, ok bool) bool { return !ok })
	writeSkillFile(t, filepath.Join(root, "after", "SKILL.md"), "---\nname: after\ndescription: After\n---\nAfter.\n")
	waitForSkill(t, store, "after", func(_ *Skill, ok bool) bool { return ok })
}

func TestStoreRejectsRootPathRedirection(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "skills")
	trustedPath := filepath.Join(root, "linked", "SKILL.md")
	writeSkillFile(t, trustedPath, "---\nname: linked\ndescription: Trusted\n---\nTrusted.\n")
	outside := t.TempDir()
	outsidePath := filepath.Join(outside, "linked", "SKILL.md")
	writeSkillFile(t, outsidePath, "---\nname: linked\ndescription: Outside\n---\nOutside.\n")

	rootHandle, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	store := &Store{
		dir:       root,
		skills:    map[string]*Skill{"linked": {Name: "linked", Description: "Trusted", Content: "Trusted.", FilePath: trustedPath, Enabled: true}},
		fileRoot:  rootHandle,
		rootBound: true,
	}
	defer store.Close()

	movedRoot := filepath.Join(parent, "trusted-moved")
	if err := os.Rename(root, movedRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, root); err != nil {
		t.Fatal(err)
	}
	outsideBefore, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatal(err)
	}

	updated, _ := store.Get("linked")
	updated.Description = "Changed"
	if err := store.Save(updated); !errors.Is(err, ErrSkillPathOutsideRoot) {
		t.Fatalf("Save() error = %v, want ErrSkillPathOutsideRoot", err)
	}
	if err := store.Remove("linked"); !errors.Is(err, ErrSkillPathOutsideRoot) {
		t.Fatalf("Remove() error = %v, want ErrSkillPathOutsideRoot", err)
	}
	outsideAfter, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(outsideAfter) != string(outsideBefore) {
		t.Fatalf("outside skill changed:\n%s", outsideAfter)
	}
}

func TestStoreCloseWaitsForWatcher(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	dir := t.TempDir()
	writeSkillFile(t, filepath.Join(dir, "example", "SKILL.md"), "---\nname: example\ndescription: Example\n---\nBody.\n")
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	store.Close()
	store.Close()
}

func writeSkillFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create skill dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
}

func assertDiagnosticCode(t *testing.T, diagnostics []SkillDiagnostic, code SkillDiagnosticCode) {
	t.Helper()
	for _, diagnostic := range diagnostics {
		if diagnostic.Type == SkillDiagnosticWarning && diagnostic.Code == code && diagnostic.Path != "" && diagnostic.Message != "" {
			return
		}
	}
	t.Fatalf("Diagnostics = %#v, missing code %q", diagnostics, code)
}

func waitForSkill(t *testing.T, store *Store, name string, match func(*Skill, bool) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		skill, ok := store.Get(name)
		if match(skill, ok) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	skill, ok := store.Get(name)
	t.Fatalf("skill %q did not reach expected state: ok=%t skill=%#v", name, ok, skill)
}
