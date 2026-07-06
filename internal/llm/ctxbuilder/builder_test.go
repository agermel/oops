package ctxbuilder

import (
	"strings"
	"testing"

	"oops/internal/llm/prompt"
	"oops/internal/llm/session"

	"github.com/cloudwego/eino/schema"
)

func TestContextBuilder_Build_NoCompactionNeeded(t *testing.T) {
	store := session.NewSessionStore()
	sess := store.Create("")

	store.AppendMessage(sess.ID, schema.UserMessage("hello"))
	store.AppendMessage(sess.ID, schema.AssistantMessage("hi", nil))

	cb := NewContextBuilder(store, nil, nil)
	cb.MaxTokens = 64000

	result := cb.Build(t.Context(), BuildOptions{
		Session:  sess,
		Question: "how are you?",
	})

	if result.Compaction != nil {
		t.Error("Build should not trigger compaction when under budget")
	}
	if len(result.Messages) < 3 {
		t.Fatalf("Messages len = %d, want at least 3", len(result.Messages))
	}
	if result.Messages[0].Role != schema.System {
		t.Errorf("first message role = %s, want system", result.Messages[0].Role)
	}
	last := result.Messages[len(result.Messages)-1]
	if last.Role != schema.User || last.Content != "how are you?" {
		t.Errorf("last message = %s: %s, want user: how are you?", last.Role, last.Content)
	}
}

func TestContextBuilder_Build_WithCompaction(t *testing.T) {
	store := session.NewSessionStore()
	sess := store.Create("")

	longMsg := strings.Repeat("z", 200)
	for range 30 {
		store.AppendMessage(sess.ID, schema.UserMessage(longMsg))
		store.AppendMessage(sess.ID, schema.AssistantMessage(longMsg, nil))
	}

	cb := NewContextBuilder(store, nil, nil)
	cb.MaxTokens = 500
	cb.KeepRecent = 4

	result := cb.Build(t.Context(), BuildOptions{
		Session:  sess,
		Question: "summarize please",
	})

	if result.Compaction == nil {
		t.Error("Build should trigger compaction when over budget")
	}
	if result.Compaction.Summary == "" {
		t.Error("compaction summary is empty")
	}
	if len(result.Messages) < 3 {
		t.Fatalf("Messages len = %d, want at least 3", len(result.Messages))
	}
	if result.Messages[0].Role != schema.System {
		t.Errorf("first message role = %s, want system", result.Messages[0].Role)
	}
}

func TestContextBuilder_prependSystemPrompt(t *testing.T) {
	cb := NewContextBuilder(session.NewSessionStore(), nil, nil)
	msgs := []*schema.Message{schema.UserMessage("hello")}

	result := cb.prependSystemPrompt(msgs, nil)
	if len(result) != 2 {
		t.Fatalf("len = %d, want 2", len(result))
	}
	if result[0].Role != schema.System {
		t.Errorf("first message role = %s, want system", result[0].Role)
	}
	if !strings.Contains(result[0].Content, "基础设施运维助手") {
		t.Error("system prompt should contain BasePrompt")
	}
}

func TestContextBuilder_prependSystemPrompt_WithProject(t *testing.T) {
	cb := NewContextBuilder(session.NewSessionStore(), nil, nil)
	msgs := []*schema.Message{schema.UserMessage("hello")}

	project := &prompt.ProjectContext{
		ID:          "proj-1",
		Name:        "TestProject",
		Description: "A test project",
		GitHubRepo:  "https://github.com/user/repo",
		NodeletIDs:  []string{"node1", "node2"},
	}

	result := cb.prependSystemPrompt(msgs, project)
	if len(result) != 2 {
		t.Fatalf("len = %d, want 2", len(result))
	}
	if !strings.Contains(result[0].Content, "TestProject") {
		t.Error("system prompt should contain project name")
	}
	if !strings.Contains(result[0].Content, "https://github.com/user/repo") {
		t.Error("system prompt should contain GitHub repo URL")
	}
	if !strings.Contains(result[0].Content, "node1") {
		t.Error("system prompt should contain nodelet IDs")
	}
	if !strings.Contains(result[0].Content, "repo_sync") {
		t.Error("system prompt should mention repo tools when repo is set")
	}
}
