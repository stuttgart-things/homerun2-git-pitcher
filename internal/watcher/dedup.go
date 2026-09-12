package watcher

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"sync"
	"time"
)

// DedupStore tracks seen event IDs to prevent re-pitching.
type DedupStore interface {
	// Seen returns true if the event ID has already been recorded.
	Seen(repo, eventID string) bool
	// Mark records an event ID as seen. createdAt is the event's creation
	// time on GitHub; retention counts from it, not from when it was seen.
	Mark(repo, eventID string, createdAt time.Time)
	// Flush persists the current state to durable storage.
	Flush() error
	// Stats returns the number of tracked events per repo.
	Stats() map[string]int
	// Retention is how far back events are tracked. The watcher never
	// pitches an event older than this, because its entry may have expired.
	Retention() time.Duration
}

// DedupConfig controls retention behaviour.
type DedupConfig struct {
	// MaxEventsPerRepo is the maximum number of event IDs to keep per repo.
	// Oldest entries are evicted when this limit is exceeded. Default: 1000.
	MaxEventsPerRepo int `yaml:"maxEventsPerRepo"`
	// Retention is how long after its creation an event ID is kept. Default: 24h.
	Retention time.Duration `yaml:"retention"`
}

// DefaultDedupConfig returns sensible defaults.
func DefaultDedupConfig() DedupConfig {
	return DedupConfig{
		MaxEventsPerRepo: 1000,
		Retention:        24 * time.Hour,
	}
}

// withDefaults fills unset fields with the defaults.
func (c DedupConfig) withDefaults() DedupConfig {
	if c.MaxEventsPerRepo <= 0 {
		c.MaxEventsPerRepo = 1000
	}
	if c.Retention <= 0 {
		c.Retention = 24 * time.Hour
	}
	return c
}

// dedupEntry holds a single seen event with its creation time.
type dedupEntry struct {
	EventID   string    `json:"eventId"`
	CreatedAt time.Time `json:"createdAt"`
	// SeenAt is only read from state files written before createdAt existed.
	SeenAt time.Time `json:"seenAt,omitzero"`
}

// MemoryDedupStore is an in-memory dedup store that can persist state to a JSON file.
type MemoryDedupStore struct {
	mu      sync.RWMutex
	entries map[string][]dedupEntry // repo -> entries
	config  DedupConfig
	path    string // file path for persistence; empty means no persistence
	now     func() time.Time
}

// NewMemoryDedupStore creates a new in-memory dedup store.
// If path is non-empty, state is loaded from and persisted to that file.
func NewMemoryDedupStore(cfg DedupConfig, path string) (*MemoryDedupStore, error) {
	s := &MemoryDedupStore{
		entries: make(map[string][]dedupEntry),
		config:  cfg.withDefaults(),
		path:    path,
		now:     time.Now,
	}

	if path != "" {
		if err := s.load(); err != nil {
			// Non-fatal: start fresh if state file is missing or corrupt.
			slog.Warn("dedup state load failed, starting fresh", "path", path, "error", err)
		}
	}

	return s, nil
}

// Seen returns true if eventID has already been recorded for repo.
func (s *MemoryDedupStore) Seen(repo, eventID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, e := range s.entries[repo] {
		if e.EventID == eventID {
			return true
		}
	}
	return false
}

// Mark records an event ID as seen and evicts old entries if needed.
func (s *MemoryDedupStore) Mark(repo, eventID string, createdAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, e := range s.entries[repo] {
		if e.EventID == eventID {
			return
		}
	}
	s.entries[repo] = append(s.entries[repo], dedupEntry{
		EventID:   eventID,
		CreatedAt: createdAt,
	})

	s.evictLocked(repo)
}

// Retention returns the configured retention window.
func (s *MemoryDedupStore) Retention() time.Duration {
	return s.config.Retention
}

// evictLocked removes entries created before the retention window and trims
// to max size, dropping the oldest. Caller must hold mu.
//
// Entries are not ordered: a poll returns events newest first, and events
// from different polls interleave. So expiry filters every entry instead of
// cutting a prefix.
func (s *MemoryDedupStore) evictLocked(repo string) {
	cutoff := s.now().Add(-s.config.Retention)

	kept := s.entries[repo][:0]
	for _, e := range s.entries[repo] {
		if !e.CreatedAt.Before(cutoff) {
			kept = append(kept, e)
		}
	}

	if len(kept) > s.config.MaxEventsPerRepo {
		sort.SliceStable(kept, func(i, j int) bool { return kept[i].CreatedAt.Before(kept[j].CreatedAt) })
		kept = kept[len(kept)-s.config.MaxEventsPerRepo:]
	}

	if len(kept) == 0 {
		delete(s.entries, repo)
		return
	}
	s.entries[repo] = kept
}

// Flush persists state to the configured file path.
func (s *MemoryDedupStore) Flush() error {
	if s.path == "" {
		return nil
	}

	s.mu.RLock()
	data, err := json.Marshal(s.entries)
	s.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("dedup flush marshal: %w", err)
	}

	if err := os.WriteFile(s.path, data, 0600); err != nil {
		return fmt.Errorf("dedup flush write: %w", err)
	}

	slog.Debug("dedup state flushed", "path", s.path)
	return nil
}

// Stats returns the count of tracked event IDs per repo.
func (s *MemoryDedupStore) Stats() map[string]int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := make(map[string]int, len(s.entries))
	for repo, entries := range s.entries {
		stats[repo] = len(entries)
	}
	return stats
}

// load reads persisted state from the file path.
func (s *MemoryDedupStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}

	var entries map[string][]dedupEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("dedup state unmarshal: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for repo, ee := range entries {
		for i := range ee {
			if ee[i].CreatedAt.IsZero() {
				ee[i].CreatedAt = ee[i].SeenAt
			}
		}
		s.entries[repo] = ee
		s.evictLocked(repo)
	}

	slog.Info("dedup state loaded", "path", s.path, "repos", len(s.entries))
	return nil
}
