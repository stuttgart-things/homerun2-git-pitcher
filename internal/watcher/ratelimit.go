package watcher

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/go-github/v68/github"
)

const (
	// DefaultBackoffThreshold is the remaining request count below which
	// the watcher pauses polling until the rate limit resets.
	DefaultBackoffThreshold = 100
)

// RateLimitStatus holds a snapshot of the current GitHub API rate limit.
type RateLimitStatus struct {
	Limit     int       `json:"limit"`
	Remaining int       `json:"remaining"`
	Reset     time.Time `json:"reset"`
	BackingOff bool    `json:"backingOff"`
}

// RateLimitMonitor tracks GitHub API rate limits across all polling goroutines
// and provides cooperative backoff when limits are low.
type RateLimitMonitor struct {
	mu        sync.RWMutex
	status    RateLimitStatus
	threshold int
}

// NewRateLimitMonitor creates a monitor with the given backoff threshold.
// If threshold <= 0, DefaultBackoffThreshold is used.
func NewRateLimitMonitor(threshold int) *RateLimitMonitor {
	if threshold <= 0 {
		threshold = DefaultBackoffThreshold
	}
	return &RateLimitMonitor{
		threshold: threshold,
	}
}

// Update records the rate limit from a GitHub API response.
func (m *RateLimitMonitor) Update(rate github.Rate) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.status = RateLimitStatus{
		Limit:      rate.Limit,
		Remaining:  rate.Remaining,
		Reset:      rate.Reset.Time,
		BackingOff: rate.Remaining < m.threshold && rate.Remaining > 0,
	}

	slog.Info("rate limit updated",
		"remaining", rate.Remaining,
		"limit", rate.Limit,
		"reset", rate.Reset.Format(time.RFC3339),
		"backingOff", m.status.BackingOff,
	)

	if m.status.BackingOff {
		slog.Warn("rate limit low, backoff active",
			"remaining", rate.Remaining,
			"threshold", m.threshold,
			"resetAt", rate.Reset.Format(time.RFC3339),
		)
	}
}

// Status returns the current rate limit snapshot.
func (m *RateLimitMonitor) Status() RateLimitStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

// WaitIfNeeded blocks until the rate limit resets if remaining requests are
// below the backoff threshold. Returns immediately if no backoff is needed.
// Respects context cancellation.
func (m *RateLimitMonitor) WaitIfNeeded(ctx context.Context) error {
	m.mu.RLock()
	status := m.status
	m.mu.RUnlock()

	if status.Remaining >= m.threshold || status.Limit == 0 {
		return nil
	}

	waitDuration := time.Until(status.Reset)
	if waitDuration <= 0 {
		return nil
	}

	// Add a small buffer to ensure the reset has actually happened.
	waitDuration += 5 * time.Second

	slog.Warn("rate limit backoff: pausing polling",
		"remaining", status.Remaining,
		"waitSeconds", int(waitDuration.Seconds()),
		"resumeAt", status.Reset.Add(5*time.Second).Format(time.RFC3339),
	)

	select {
	case <-time.After(waitDuration):
		slog.Info("rate limit backoff complete, resuming polling")
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
