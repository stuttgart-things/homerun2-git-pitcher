package watcher

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMemoryDedupStore_SeenAndMark(t *testing.T) {
	s, err := NewMemoryDedupStore(DefaultDedupConfig(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	repo := "org/repo"
	if s.Seen(repo, "evt-1") {
		t.Error("expected evt-1 to not be seen")
	}

	s.Mark(repo, "evt-1")
	if !s.Seen(repo, "evt-1") {
		t.Error("expected evt-1 to be seen after Mark")
	}

	// Different repo should not see evt-1.
	if s.Seen("other/repo", "evt-1") {
		t.Error("expected evt-1 to not be seen in other repo")
	}
}

func TestMemoryDedupStore_MaxEventsEviction(t *testing.T) {
	cfg := DedupConfig{MaxEventsPerRepo: 3, Retention: 24 * time.Hour}
	s, err := NewMemoryDedupStore(cfg, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	repo := "org/repo"
	s.Mark(repo, "evt-1")
	s.Mark(repo, "evt-2")
	s.Mark(repo, "evt-3")
	s.Mark(repo, "evt-4") // should evict evt-1

	if s.Seen(repo, "evt-1") {
		t.Error("expected evt-1 to be evicted")
	}
	if !s.Seen(repo, "evt-4") {
		t.Error("expected evt-4 to be seen")
	}

	stats := s.Stats()
	if stats[repo] != 3 {
		t.Errorf("expected 3 entries, got %d", stats[repo])
	}
}

func TestMemoryDedupStore_RetentionEviction(t *testing.T) {
	cfg := DedupConfig{MaxEventsPerRepo: 1000, Retention: 100 * time.Millisecond}
	s, err := NewMemoryDedupStore(cfg, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	repo := "org/repo"
	s.Mark(repo, "old-evt")

	// Wait for the entry to expire.
	time.Sleep(150 * time.Millisecond)

	// Mark a new event to trigger eviction.
	s.Mark(repo, "new-evt")

	if s.Seen(repo, "old-evt") {
		t.Error("expected old-evt to be evicted by retention")
	}
	if !s.Seen(repo, "new-evt") {
		t.Error("expected new-evt to be seen")
	}
}

func TestMemoryDedupStore_Persistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dedup-state.json")
	cfg := DefaultDedupConfig()

	// Create store, mark events, flush.
	s1, err := NewMemoryDedupStore(cfg, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s1.Mark("org/repo", "evt-1")
	s1.Mark("org/repo", "evt-2")
	s1.Mark("other/repo", "evt-a")

	if err := s1.Flush(); err != nil {
		t.Fatalf("flush failed: %v", err)
	}

	// Verify file was written.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("state file not created: %v", err)
	}

	// Load into new store.
	s2, err := NewMemoryDedupStore(cfg, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !s2.Seen("org/repo", "evt-1") {
		t.Error("expected evt-1 to survive persistence")
	}
	if !s2.Seen("org/repo", "evt-2") {
		t.Error("expected evt-2 to survive persistence")
	}
	if !s2.Seen("other/repo", "evt-a") {
		t.Error("expected evt-a to survive persistence")
	}
	if s2.Seen("org/repo", "evt-never") {
		t.Error("expected evt-never to not be seen")
	}
}

func TestMemoryDedupStore_PersistenceFilterExpired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dedup-state.json")
	cfg := DedupConfig{MaxEventsPerRepo: 1000, Retention: 100 * time.Millisecond}

	s1, err := NewMemoryDedupStore(cfg, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s1.Mark("org/repo", "old-evt")
	if err := s1.Flush(); err != nil {
		t.Fatalf("flush failed: %v", err)
	}

	// Wait for entry to expire.
	time.Sleep(150 * time.Millisecond)

	// Load: expired entries should be filtered out.
	s2, err := NewMemoryDedupStore(cfg, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if s2.Seen("org/repo", "old-evt") {
		t.Error("expected old-evt to be filtered on load")
	}
}

func TestMemoryDedupStore_FlushNoPath(t *testing.T) {
	s, err := NewMemoryDedupStore(DefaultDedupConfig(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s.Mark("org/repo", "evt-1")

	// Flush with no path should be a no-op.
	if err := s.Flush(); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestMemoryDedupStore_Stats(t *testing.T) {
	s, err := NewMemoryDedupStore(DefaultDedupConfig(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	s.Mark("org/repo-a", "1")
	s.Mark("org/repo-a", "2")
	s.Mark("org/repo-b", "3")

	stats := s.Stats()
	if stats["org/repo-a"] != 2 {
		t.Errorf("expected 2 for repo-a, got %d", stats["org/repo-a"])
	}
	if stats["org/repo-b"] != 1 {
		t.Errorf("expected 1 for repo-b, got %d", stats["org/repo-b"])
	}
}

func TestMemoryDedupStore_DefaultConfig(t *testing.T) {
	// Zero config should get defaults applied.
	s, err := NewMemoryDedupStore(DedupConfig{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Mark more than default max (1000) to verify the limit works.
	for i := range 1005 {
		s.Mark("org/repo", fmt.Sprintf("evt-%d", i))
	}

	stats := s.Stats()
	if stats["org/repo"] != 1000 {
		t.Errorf("expected 1000 entries after overflow, got %d", stats["org/repo"])
	}

	// Oldest 5 should be evicted.
	if s.Seen("org/repo", "evt-0") {
		t.Error("expected evt-0 to be evicted")
	}
	if !s.Seen("org/repo", "evt-1004") {
		t.Error("expected evt-1004 to be seen")
	}
}
