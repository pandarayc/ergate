package session

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/raydraw/ergate/internal/llm"
)

func newTestService(t *testing.T) *FileService {
	t.Helper()
	svc, err := NewFileService(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestServiceCreateAndGet(t *testing.T) {
	svc := newTestService(t)

	// Create.
	createResp, err := svc.Create(context.Background(), &CreateRequest{
		AppName:   "test",
		UserID:    "user1",
		SessionID: "test_session",
	})
	if err != nil {
		t.Fatal(err)
	}
	if createResp.Session.ID != "test_session" {
		t.Errorf("ID: got %q, want %q", createResp.Session.ID, "test_session")
	}

	// Create duplicate should fail.
	_, err = svc.Create(context.Background(), &CreateRequest{
		AppName:   "test",
		UserID:    "user1",
		SessionID: "test_session",
	})
	if err == nil {
		t.Error("expected error creating duplicate session")
	}
}

func TestServiceAppendEvent(t *testing.T) {
	svc := newTestService(t)

	resp, _ := svc.Create(context.Background(), &CreateRequest{
		AppName:   "test",
		UserID:    "user1",
		SessionID: "events_test",
	})

	// Append a user event.
	err := svc.AppendEvent(context.Background(), resp.Session, &Event{
		ID:        "evt-1",
		Timestamp: time.Now(),
		Author:    "user",
		Message:   llm.Message{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "hello"}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Append an assistant event.
	err = svc.AppendEvent(context.Background(), resp.Session, &Event{
		ID:        "evt-2",
		Timestamp: time.Now(),
		Author:    "assistant",
		Message:   llm.Message{Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: "hi!"}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Partial event should not be persisted.
	err = svc.AppendEvent(context.Background(), resp.Session, &Event{
		ID:      "evt-3",
		Author:  "assistant",
		Message: llm.Message{Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: "partial..."}}},
		Partial: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify persistence.
	getResp, err := svc.Get(context.Background(), &GetRequest{
		AppName:   "test",
		UserID:    "user1",
		SessionID: "events_test",
	})
	if err != nil {
		t.Fatal(err)
	}

	sess := getResp.Session
	if len(sess.Messages) != 2 {
		t.Errorf("Messages: got %d, want 2 (partial should not persist)", len(sess.Messages))
	}
	if len(sess.Events) != 2 {
		t.Errorf("Events: got %d, want 2", len(sess.Events))
	}
}

func TestServiceListAndDelete(t *testing.T) {
	svc := newTestService(t)

	svc.Create(context.Background(), &CreateRequest{AppName: "t", UserID: "u", SessionID: "b"})
	svc.Create(context.Background(), &CreateRequest{AppName: "t", UserID: "u", SessionID: "a"})

	listResp, err := svc.List(context.Background(), &ListRequest{AppName: "t", UserID: "u"})
	if err != nil {
		t.Fatal(err)
	}
	if len(listResp.Sessions) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(listResp.Sessions))
	}
	// Most recent first (b created before a, but a modified later in file system).
	// Order depends on file mtime; just check count.

	// Delete.
	err = svc.Delete(context.Background(), &DeleteRequest{AppName: "t", UserID: "u", SessionID: "a"})
	if err != nil {
		t.Fatal(err)
	}

	listResp, _ = svc.List(context.Background(), &ListRequest{AppName: "t", UserID: "u"})
	if len(listResp.Sessions) != 1 {
		t.Errorf("expected 1 session after delete, got %d", len(listResp.Sessions))
	}
}

func TestServiceGetWithNumRecentEvents(t *testing.T) {
	svc := newTestService(t)

	resp, _ := svc.Create(context.Background(), &CreateRequest{SessionID: "filter_test"})

	for i := 0; i < 10; i++ {
		svc.AppendEvent(context.Background(), resp.Session, &Event{
			ID:        fmt.Sprintf("evt-%d", i),
			Timestamp: time.Now(),
			Author:    "user",
			Message:   llm.Message{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: fmt.Sprintf("msg %d", i)}}},
		})
	}

	getResp, err := svc.Get(context.Background(), &GetRequest{
		SessionID:       "filter_test",
		NumRecentEvents: 3,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(getResp.Session.Events) != 3 {
		t.Errorf("NumRecentEvents=3: got %d events", len(getResp.Session.Events))
	}
}

func TestServiceAutoGenerateID(t *testing.T) {
	svc := newTestService(t)

	resp, err := svc.Create(context.Background(), &CreateRequest{
		AppName: "test",
		UserID:  "user1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Session.ID == "" {
		t.Error("expected auto-generated session ID")
	}
}

// --- Legacy Store tests (backward compatible) ---

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	sess := &Session{
		ID:        "test_session",
		CreatedAt: time.Now(),
		Model:     "claude-test",
		Messages: []llm.Message{
			{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "hello"}}},
			{Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: "hi!"}}},
		},
		Usage: llm.Usage{InputTokens: 5, OutputTokens: 3},
	}

	if err := store.Save(context.Background(), sess); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load("test_session")
	if err != nil {
		t.Fatal(err)
	}

	if loaded.Model != "claude-test" {
		t.Errorf("model: got %q, want %q", loaded.Model, "claude-test")
	}
	if len(loaded.Messages) != 2 {
		t.Errorf("messages: got %d, want 2", len(loaded.Messages))
	}
	if loaded.Usage.InputTokens != 5 {
		t.Errorf("input tokens: got %d, want 5", loaded.Usage.InputTokens)
	}
}

func TestListSessions(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)

	store.Save(context.Background(), &Session{ID: "b", CreatedAt: time.Now().Add(-1 * time.Hour)})
	store.Save(context.Background(), &Session{ID: "a", CreatedAt: time.Now()})

	// Use Service.List for session list
	resp, err := store.List(context.Background(), &ListRequest{})
	if err != nil {
		t.Fatal(err)
	}

	if len(resp.Sessions) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(resp.Sessions))
	}
}

func TestDeleteSession(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)

	store.Save(context.Background(), &Session{ID: "to_delete"})
	store.Delete(context.Background(), &DeleteRequest{SessionID: "to_delete"})

	_, err := store.Load("to_delete")
	if err == nil {
		t.Error("expected error loading deleted session")
	}
	if !os.IsNotExist(err) {
		t.Logf("error type: %v", err)
	}
}

func TestLatestSession(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)

	sess, err := store.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if sess != nil {
		t.Error("expected nil for empty store")
	}

	store.Save(context.Background(), &Session{ID: "latest", CreatedAt: time.Now()})
	sess, err = store.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID != "latest" {
		t.Errorf("expected 'latest', got %q", sess.ID)
	}
}

func TestEngineExportImport(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)

	sess := &Session{
		ID:    "engine_test",
		Model: "test",
		Messages: []llm.Message{
			{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "test"}}},
		},
	}

	store.Save(context.Background(), sess)
	loaded, _ := store.Load("engine_test")
	if len(loaded.Messages) != 1 {
		t.Errorf("messages: got %d", len(loaded.Messages))
	}
}
