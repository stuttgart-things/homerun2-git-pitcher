package watcher

import (
	"testing"
	"time"

	"github.com/google/go-github/v68/github"
)

func TestEventTypeToKind(t *testing.T) {
	tests := []struct {
		ghType string
		want   EventKind
	}{
		{"PushEvent", EventPush},
		{"PullRequestEvent", EventPullRequest},
		{"ReleaseEvent", EventRelease},
		{"WorkflowRunEvent", EventWorkflowRun},
		{"ForkEvent", ""},
		{"", ""},
	}

	for _, tt := range tests {
		got := eventTypeToKind(tt.ghType)
		if got != tt.want {
			t.Errorf("eventTypeToKind(%q) = %q, want %q", tt.ghType, got, tt.want)
		}
	}
}

func TestEventToMessage(t *testing.T) {
	now := time.Now()
	login := "testuser"
	actor := &github.User{Login: &login}
	eventType := "PushEvent"
	eventID := "12345"
	ts := github.Timestamp{Time: now}

	event := &github.Event{
		ID:        &eventID,
		Type:      &eventType,
		Actor:     actor,
		CreatedAt: &ts,
	}

	repo := RepoConfig{
		Owner: "stuttgart-things",
		Name:  "homerun2-led-catcher",
	}

	msg := eventToMessage(event, repo)

	if msg.Author != "testuser" {
		t.Errorf("expected author 'testuser', got %q", msg.Author)
	}
	if msg.System != "homerun2-git-pitcher" {
		t.Errorf("expected system 'homerun2-git-pitcher', got %q", msg.System)
	}
	if msg.Severity != "info" {
		t.Errorf("expected severity 'info', got %q", msg.Severity)
	}
	if msg.Title == "" {
		t.Error("expected non-empty title")
	}
	if msg.Url != "https://github.com/stuttgart-things/homerun2-led-catcher" {
		t.Errorf("unexpected url: %s", msg.Url)
	}
}

func TestNewGitHubWatcher(t *testing.T) {
	cfg := &WatchConfig{
		GitHub: GitHubConfig{
			Token: "test-token",
			Repos: []RepoConfig{
				{Owner: "org", Name: "repo", Interval: 5 * time.Minute, Events: []EventKind{EventPush}},
			},
		},
	}

	w := NewGitHubWatcher(cfg, nil)

	if w.client == nil {
		t.Error("expected non-nil client")
	}
	if w.config != cfg {
		t.Error("expected config to be set")
	}
	if w.dedup == nil {
		t.Error("expected default dedup store")
	}
	// First run should be true for repo with no persisted state.
	if !w.firstRun["org/repo"] {
		t.Error("expected firstRun to be true for new repo")
	}
}

func TestNewGitHubWatcher_WithPersistedDedup(t *testing.T) {
	cfg := &WatchConfig{
		GitHub: GitHubConfig{
			Token: "test-token",
			Repos: []RepoConfig{
				{Owner: "org", Name: "repo", Interval: 5 * time.Minute, Events: []EventKind{EventPush}},
			},
		},
	}

	// Pre-populate dedup store to simulate persisted state.
	dedup, _ := NewMemoryDedupStore(DefaultDedupConfig(), "")
	dedup.Mark("org/repo", "existing-event")

	w := NewGitHubWatcher(cfg, dedup)

	// firstRun should be false since dedup has state for this repo.
	if w.firstRun["org/repo"] {
		t.Error("expected firstRun to be false when dedup has persisted state")
	}
}
