package watcher

import (
	"context"
	"sync"
	"testing"

	homerun "github.com/stuttgart-things/homerun-library/v4"
)

// mockPitcher records all pitched messages and their stream overrides.
type mockPitcher struct {
	mu       sync.Mutex
	messages []homerun.Message
	streams  []string
}

func (m *mockPitcher) Pitch(msg homerun.Message, streamOverride string) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, msg)
	m.streams = append(m.streams, streamOverride)
	return "obj-1", "stream-1", nil
}

// mockWatcher sends predefined events then closes the channel.
type mockWatcher struct {
	events []PitchEvent
}

func (m *mockWatcher) Watch(_ context.Context) (<-chan PitchEvent, error) {
	ch := make(chan PitchEvent, len(m.events))
	for _, ev := range m.events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func TestBridge_Run(t *testing.T) {
	testEvents := []PitchEvent{
		{Message: homerun.Message{Title: "Push to main on org/repo", Severity: "info", System: "homerun2-git-pitcher"}},
		{Message: homerun.Message{Title: "PR #42: Add feature (opened)", Severity: "info", System: "homerun2-git-pitcher"}},
		{Message: homerun.Message{Title: "Release v1.0.0 on org/repo", Severity: "success", System: "homerun2-git-pitcher"}, Stream: "releases"},
	}

	p := &mockPitcher{}
	w := &mockWatcher{events: testEvents}
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
	if p.streams[0] != "" {
		t.Errorf("expected empty stream override for push, got %q", p.streams[0])
	}
	if p.streams[2] != "releases" {
		t.Errorf("expected stream override 'releases' for release, got %q", p.streams[2])
	}
}
