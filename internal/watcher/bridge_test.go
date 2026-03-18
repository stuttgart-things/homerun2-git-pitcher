package watcher

import (
	"context"
	"sync"
	"testing"

	homerun "github.com/stuttgart-things/homerun-library/v3"
)

// mockPitcher records all pitched messages.
type mockPitcher struct {
	mu       sync.Mutex
	messages []homerun.Message
}

func (m *mockPitcher) Pitch(msg homerun.Message) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, msg)
	return "obj-1", "stream-1", nil
}

// mockWatcher sends predefined messages then closes the channel.
type mockWatcher struct {
	msgs []homerun.Message
}

func (m *mockWatcher) Watch(_ context.Context) (<-chan homerun.Message, error) {
	ch := make(chan homerun.Message, len(m.msgs))
	for _, msg := range m.msgs {
		ch <- msg
	}
	close(ch)
	return ch, nil
}

func TestBridge_Run(t *testing.T) {
	testMsgs := []homerun.Message{
		{Title: "Push to main on org/repo", Severity: "info", System: "homerun2-git-pitcher"},
		{Title: "PR #42: Add feature (opened)", Severity: "info", System: "homerun2-git-pitcher"},
		{Title: "Release v1.0.0 on org/repo", Severity: "success", System: "homerun2-git-pitcher"},
	}

	p := &mockPitcher{}
	w := &mockWatcher{msgs: testMsgs}
	dedup, _ := NewMemoryDedupStore(DefaultDedupConfig(), "")

	bridge := &Bridge{
		Watcher: w,
		Pitcher: p,
		Dedup:   dedup,
	}

	ctx := context.Background()
	if err := bridge.Run(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.messages) != 3 {
		t.Fatalf("expected 3 pitched messages, got %d", len(p.messages))
	}
	if p.messages[0].Title != "Push to main on org/repo" {
		t.Errorf("unexpected first message title: %s", p.messages[0].Title)
	}
	if p.messages[2].Severity != "success" {
		t.Errorf("expected severity 'success' for release, got %q", p.messages[2].Severity)
	}
}
