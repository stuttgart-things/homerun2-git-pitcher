package watcher

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

// DedupStore tracks seen event IDs to prevent re-pitching.
type DedupStore interface {
	// Seen returns true if the event ID has already been recorded.
	Seen(repo, eventID string) bool
	// Mark records an event ID as seen.
	Mark(repo, eventID string)
	// Flush persists the current state to durable storage.
	Flush() error
	// Stats returns the number of tracked events per repo.
	Stats() map[string]int
}

// DedupConfig controls retention behaviour.
type DedupConfig struct {
	// MaxEventsPerRepo is the maximum number of event IDs to keep per repo.
	// Oldest entries are evicted when this limit is exceeded. Default: 1000.
	MaxEventsPerRepo int `yaml:"maxEventsPerRepo"`
	// Retention is how long event IDs are kept before expiry. Default: 24h.
	Retention time.Duration `yaml:"retention"`
}

// DefaultDedupConfig returns sensible defaults.
func DefaultDedupConfig() DedupConfig {
	return DedupConfig{
		MaxEventsPerRepo: 1000,
		Retention:        24 * time.Hour,
	}
}

// dedupEntry holds a single seen event with its timestamp.
type dedupEntry struct {
	EventID string    `json:"eventId"`
	SeenAt  time.Time `json:"seenAt"`
}

// MemoryDedupStore is an in-memory dedup store that can persist state to a JSON file.
type MemoryDedupStore struct {
	mu      sync.RWMutex
	entries map[string][]dedupEntry // repo -> ordered entries (oldest first)
	config  DedupConfig
	path    string // file path for persistence; empty means no persistence
}

// NewMemoryDedupStore creates a new in-memory dedup store.
// If path is non-empty, state is loaded from and persisted to that file.
func NewMemoryDedupStore(cfg DedupConfig, path string) (*MemoryDedupStore, error) {
	if cfg.MaxEventsPerRepo <= 0 {
		cfg.MaxEventsPerRepo = 1000
	}
	if cfg.Retention <= 0 {
		cfg.Retention = 24 * time.Hour
	}

	s := &MemoryDedupStore{
		entries: make(map[string][]dedupEntry),
		config:  cfg,
		path:    path,
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
func (s *MemoryDedupStore) Mark(repo, eventID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Append new entry.
	s.entries[repo] = append(s.entries[repo], dedupEntry{
		EventID: eventID,
		SeenAt:  time.Now(),
	})

	s.evictLocked(repo)
}

// evictLocked removes expired entries and trims to max size. Caller must hold mu.
func (s *MemoryDedupStore) evictLocked(repo string) {
	entries := s.entries[repo]
	cutoff := time.Now().Add(-s.config.Retention)

	// Remove expired entries (entries are ordered oldest-first).
	start := 0
	for start < len(entries) && entries[start].SeenAt.Before(cutoff) {
		start++
	}
	entries = entries[start:]

	// Trim to max size.
	if len(entries) > s.config.MaxEventsPerRepo {
		entries = entries[len(entries)-s.config.MaxEventsPerRepo:]
	}

	s.entries[repo] = entries
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

	// Filter out expired entries on load.
	cutoff := time.Now().Add(-s.config.Retention)
	for repo, ee := range entries {
		var valid []dedupEntry
		for _, e := range ee {
			if e.SeenAt.After(cutoff) {
				valid = append(valid, e)
			}
		}
		if len(valid) > 0 {
			entries[repo] = valid
		} else {
			delete(entries, repo)
		}
	}

	s.entries = entries
	slog.Info("dedup state loaded", "path", s.path, "repos", len(entries))
	return nil
}
