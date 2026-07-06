package session

import (
	"sync"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestSessionStore_Create(t *testing.T) {
	store := NewSessionStore()
	sess := store.Create("")

	if sess.ID == "" {
		t.Fatal("Create() session ID is empty")
	}
	if len(sess.Messages) != 0 {
		t.Fatalf("len(Messages) = %d, want 0", len(sess.Messages))
	}
}

func TestSessionStore_GetOrCreate(t *testing.T) {
	store := NewSessionStore()

	// 首次调用：创建。
	s1 := store.GetOrCreate("my-id", "proj1")
	if s1.ID != "my-id" {
		t.Fatalf("ID = %q, want my-id", s1.ID)
	}

	// 再次调用：返回已有 session。
	s2 := store.GetOrCreate("my-id", "proj2")
	if s2.ProjectID != "proj1" {
		t.Fatalf("ProjectID = %q, want proj1 (must return existing)", s2.ProjectID)
	}
}

func TestSessionStore_AppendMessage(t *testing.T) {
	store := NewSessionStore()
	sess := store.Create("")

	msg := schema.UserMessage("hello")
	store.AppendMessage(sess.ID, msg)

	got, ok := store.Get(sess.ID)
	if !ok {
		t.Fatal("Get() returned false")
	}
	if len(got.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1", len(got.Messages))
	}
	if got.Messages[0].Content != "hello" {
		t.Fatalf("Content = %q, want hello", got.Messages[0].Content)
	}
}

func TestSessionStore_AppendMessage_NoPanicOnMissingSession(t *testing.T) {
	store := NewSessionStore()
	// 不应该 panic。
	store.AppendMessage("nonexistent", schema.UserMessage("hello"))
}

func TestSessionStore_List(t *testing.T) {
	store := NewSessionStore()
	store.Create("")       // global
	store.Create("proj-a") // project A
	store.Create("proj-a") // project A (second)
	store.Create("proj-b") // project B

	if n := len(store.List("")); n != 1 {
		t.Fatalf("global = %d, want 1", n)
	}
	if n := len(store.List("proj-a")); n != 2 {
		t.Fatalf("proj-a = %d, want 2", n)
	}
	if n := len(store.List("*")); n != 4 {
		t.Fatalf("all = %d, want 4", n)
	}
}

func TestSessionStore_Delete(t *testing.T) {
	store := NewSessionStore()
	sess := store.Create("")

	if !store.Delete(sess.ID) {
		t.Fatal("Delete() returned false for existing session")
	}
	if store.Delete(sess.ID) {
		t.Fatal("Delete() returned true for already-deleted session")
	}
	if _, ok := store.Get(sess.ID); ok {
		t.Fatal("Get() returned true after delete")
	}
}

func TestSessionStore_Concurrent(t *testing.T) {
	store := NewSessionStore()
	sess := store.Create("")

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			store.AppendMessage(sess.ID, schema.UserMessage("msg"))
		}()
	}
	wg.Wait()

	got, ok := store.Get(sess.ID)
	if !ok {
		t.Fatal("Get() returned false after concurrent appends")
	}
	if len(got.Messages) != 50 {
		t.Fatalf("len(Messages) = %d, want 50", len(got.Messages))
	}
}
