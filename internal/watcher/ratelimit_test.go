package watcher

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-github/v68/github"
)

func TestRateLimitMonitor_Update(t *testing.T) {
	m := NewRateLimitMonitor(100)

	rate := github.Rate{
		Limit:     5000,
		Remaining: 4500,
		Reset:     github.Timestamp{Time: time.Now().Add(30 * time.Minute)},
	}
	m.Update(rate)

	s := m.Status()
	if s.Limit != 5000 {
		t.Errorf("expected limit 5000, got %d", s.Limit)
	}
	if s.Remaining != 4500 {
		t.Errorf("expected remaining 4500, got %d", s.Remaining)
	}
	if s.BackingOff {
		t.Error("should not be backing off with 4500 remaining")
	}
}

func TestRateLimitMonitor_BackoffTriggered(t *testing.T) {
	m := NewRateLimitMonitor(100)

	rate := github.Rate{
		Limit:     5000,
		Remaining: 50,
		Reset:     github.Timestamp{Time: time.Now().Add(10 * time.Minute)},
	}
	m.Update(rate)

	s := m.Status()
	if !s.BackingOff {
		t.Error("expected backing off with 50 remaining (threshold 100)")
	}
}

func TestRateLimitMonitor_WaitIfNeeded_NoBackoff(t *testing.T) {
	m := NewRateLimitMonitor(100)

	rate := github.Rate{
		Limit:     5000,
		Remaining: 4000,
		Reset:     github.Timestamp{Time: time.Now().Add(30 * time.Minute)},
	}
	m.Update(rate)

	ctx := context.Background()
	start := time.Now()
	err := m.WaitIfNeeded(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Error("WaitIfNeeded should return immediately when not backing off")
	}
}

func TestRateLimitMonitor_WaitIfNeeded_Backoff(t *testing.T) {
	m := NewRateLimitMonitor(100)

	// Set reset to a very short time from now.
	rate := github.Rate{
		Limit:     5000,
		Remaining: 10,
		Reset:     github.Timestamp{Time: time.Now().Add(100 * time.Millisecond)},
	}
	m.Update(rate)

	ctx := context.Background()
	start := time.Now()
	err := m.WaitIfNeeded(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should wait at least ~100ms (reset) + 5s buffer, but we set reset very close.
	elapsed := time.Since(start)
	if elapsed < 100*time.Millisecond {
		t.Errorf("expected to wait at least 100ms, waited %s", elapsed)
	}
}

func TestRateLimitMonitor_WaitIfNeeded_ContextCancel(t *testing.T) {
	m := NewRateLimitMonitor(100)

	rate := github.Rate{
		Limit:     5000,
		Remaining: 10,
		Reset:     github.Timestamp{Time: time.Now().Add(1 * time.Hour)},
	}
	m.Update(rate)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := m.WaitIfNeeded(ctx)
	if err != context.DeadlineExceeded {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
}

func TestRateLimitMonitor_WaitIfNeeded_ResetInPast(t *testing.T) {
	m := NewRateLimitMonitor(100)

	// Reset is already in the past — should return immediately.
	rate := github.Rate{
		Limit:     5000,
		Remaining: 10,
		Reset:     github.Timestamp{Time: time.Now().Add(-10 * time.Second)},
	}
	m.Update(rate)

	ctx := context.Background()
	start := time.Now()
	err := m.WaitIfNeeded(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Error("should return immediately when reset is in the past")
	}
}

func TestRateLimitMonitor_DefaultThreshold(t *testing.T) {
	m := NewRateLimitMonitor(0)
	if m.threshold != DefaultBackoffThreshold {
		t.Errorf("expected default threshold %d, got %d", DefaultBackoffThreshold, m.threshold)
	}
}

func TestRateLimitMonitor_ZeroLimit(t *testing.T) {
	m := NewRateLimitMonitor(100)
	// No update yet — limit is 0, should not backoff.
	ctx := context.Background()
	err := m.WaitIfNeeded(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
