package tui_test

import (
	"encoding/json"
	"testing"

	"tui-canvas/internal/protocol"
	"tui-canvas/internal/tui"
)

// newTestModel creates a Model suitable for unit tests (no real connection).
func newTestModel(t *testing.T) *tui.Model {
	t.Helper()
	m, err := tui.NewTestModel()
	if err != nil {
		t.Fatalf("NewTestModel: %v", err)
	}
	return m
}

func TestHandleSocketMsg_FullState(t *testing.T) {
	m := newTestModel(t)
	sessions := []protocol.Session{
		{ID: "s1", Name: "proj1", CWD: "/tmp/a"},
		{ID: "s2", Name: "proj2", CWD: "/tmp/b"},
	}
	raw, _ := json.Marshal(protocol.FullState{Type: protocol.TypeFullState, Sessions: sessions})
	m.HandleSocketMsgForTest(raw)

	got := m.Sessions()
	if len(got) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(got))
	}
}

func TestHandleSocketMsg_SessionAdded(t *testing.T) {
	m := newTestModel(t)
	raw, _ := json.Marshal(protocol.SessionAdded{
		Type: protocol.TypeSessionAdded, SessionID: "s1", Name: "proj", CWD: "/tmp",
	})
	m.HandleSocketMsgForTest(raw)

	if len(m.Sessions()) != 1 {
		t.Fatalf("expected 1 session, got %d", len(m.Sessions()))
	}
}

func TestHandleSocketMsg_CanvasAppended(t *testing.T) {
	m := newTestModel(t)

	// Register session first via full_state
	sessions := []protocol.Session{{ID: "s1", Name: "proj", CWD: "/tmp"}}
	raw, _ := json.Marshal(protocol.FullState{Type: protocol.TypeFullState, Sessions: sessions})
	m.HandleSocketMsgForTest(raw)

	raw, _ = json.Marshal(protocol.CanvasAppended{
		Type:      protocol.TypeCanvasAppended,
		SessionID: "s1",
		Entry:     protocol.Entry{Content: "hello", Index: 1},
	})
	m.HandleSocketMsgForTest(raw)

	s := m.Sessions()
	if len(s[0].Entries) != 1 || s[0].Entries[0].Content != "hello" {
		t.Errorf("expected entry with content 'hello', got %+v", s[0].Entries)
	}
}

func TestHandleSocketMsg_CanvasCleared(t *testing.T) {
	m := newTestModel(t)

	sessions := []protocol.Session{{ID: "s1", Name: "proj", CWD: "/tmp", Entries: []protocol.Entry{{Content: "x", Index: 1}}}}
	raw, _ := json.Marshal(protocol.FullState{Type: protocol.TypeFullState, Sessions: sessions})
	m.HandleSocketMsgForTest(raw)

	raw, _ = json.Marshal(protocol.CanvasCleared{Type: protocol.TypeCanvasCleared, SessionID: "s1"})
	m.HandleSocketMsgForTest(raw)

	if len(m.Sessions()[0].Entries) != 0 {
		t.Error("expected 0 entries after clear")
	}
}

func TestHandleSocketMsg_SessionRemoved(t *testing.T) {
	m := newTestModel(t)

	sessions := []protocol.Session{
		{ID: "s1", Name: "proj1", CWD: "/tmp/a"},
		{ID: "s2", Name: "proj2", CWD: "/tmp/b"},
	}
	raw, _ := json.Marshal(protocol.FullState{Type: protocol.TypeFullState, Sessions: sessions})
	m.HandleSocketMsgForTest(raw)

	raw, _ = json.Marshal(protocol.SessionRemoved{Type: protocol.TypeSessionRemoved, SessionID: "s1"})
	m.HandleSocketMsgForTest(raw)

	if len(m.Sessions()) != 1 || m.Sessions()[0].ID != "s2" {
		t.Errorf("expected only s2, got %+v", m.Sessions())
	}
}

func TestSearch_EnterAndClear(t *testing.T) {
	m := newTestModel(t)
	m.EnterSearchForTest()
	if !m.SearchingForTest() {
		t.Fatal("expected searching=true after EnterSearch")
	}
	m.ClearSearchForTest()
	if m.SearchingForTest() {
		t.Fatal("expected searching=false after ClearSearch")
	}
}

func TestSearch_UpdateSearch_NoQuery(t *testing.T) {
	m := newTestModel(t)
	m.UpdateSearchForTest("")
	if len(m.SearchHitsForTest()) != 0 {
		t.Error("expected no hits for empty query")
	}
}
