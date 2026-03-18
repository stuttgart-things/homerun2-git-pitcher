package watcher

import (
	"encoding/json"
	"strings"
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

func TestEventToMessage_Push(t *testing.T) {
	repo := RepoConfig{Owner: "org", Name: "repo"}
	pushPayload := github.PushEvent{
		Ref: github.Ptr("refs/heads/main"),
		Commits: []*github.HeadCommit{
			{Message: github.Ptr("fix: something")},
		},
		HeadCommit: &github.HeadCommit{Message: github.Ptr("fix: something")},
		Pusher:     &github.CommitAuthor{Name: github.Ptr("testuser")},
		Compare:    github.Ptr("https://github.com/org/repo/compare/abc...def"),
	}
	raw, _ := json.Marshal(pushPayload)
	event := makeEvent("PushEvent", "testuser", raw)

	msg := eventToMessage(event, repo)

	if !strings.Contains(msg.Title, "Push to main") {
		t.Errorf("expected push title with branch, got %q", msg.Title)
	}
	if msg.Severity != "info" {
		t.Errorf("expected severity 'info', got %q", msg.Severity)
	}
	if msg.Url != "https://github.com/org/repo/compare/abc...def" {
		t.Errorf("unexpected url: %s", msg.Url)
	}
}

func TestEventToMessage_PullRequest(t *testing.T) {
	repo := RepoConfig{Owner: "org", Name: "repo"}
	prPayload := github.PullRequestEvent{
		Action: github.Ptr("opened"),
		PullRequest: &github.PullRequest{
			Number:  github.Ptr(42),
			Title:   github.Ptr("Add feature X"),
			HTMLURL: github.Ptr("https://github.com/org/repo/pull/42"),
			User:    &github.User{Login: github.Ptr("dev")},
			Head:    &github.PullRequestBranch{Ref: github.Ptr("feat/x")},
			Base:    &github.PullRequestBranch{Ref: github.Ptr("main")},
		},
	}
	raw, _ := json.Marshal(prPayload)
	event := makeEvent("PullRequestEvent", "dev", raw)

	msg := eventToMessage(event, repo)

	if !strings.Contains(msg.Title, "PR #42") {
		t.Errorf("expected PR number in title, got %q", msg.Title)
	}
	if !strings.Contains(msg.Title, "opened") {
		t.Errorf("expected action in title, got %q", msg.Title)
	}
	if msg.Url != "https://github.com/org/repo/pull/42" {
		t.Errorf("unexpected url: %s", msg.Url)
	}
}

func TestEventToMessage_Release(t *testing.T) {
	repo := RepoConfig{Owner: "org", Name: "repo"}
	relPayload := github.ReleaseEvent{
		Release: &github.RepositoryRelease{
			TagName: github.Ptr("v1.2.3"),
			Name:    github.Ptr("Release v1.2.3"),
			Body:    github.Ptr("Changelog here"),
			HTMLURL: github.Ptr("https://github.com/org/repo/releases/tag/v1.2.3"),
		},
	}
	raw, _ := json.Marshal(relPayload)
	event := makeEvent("ReleaseEvent", "releaser", raw)

	msg := eventToMessage(event, repo)

	if !strings.Contains(msg.Title, "Release v1.2.3") {
		t.Errorf("expected release tag in title, got %q", msg.Title)
	}
	if msg.Severity != "success" {
		t.Errorf("expected severity 'success', got %q", msg.Severity)
	}
}

func TestEventToMessage_WorkflowRun(t *testing.T) {
	repo := RepoConfig{Owner: "org", Name: "repo"}
	wrPayload := github.WorkflowRunEvent{
		WorkflowRun: &github.WorkflowRun{
			Name:       github.Ptr("CI"),
			Conclusion: github.Ptr("failure"),
			RunNumber:  github.Ptr(99),
			HeadBranch: github.Ptr("main"),
			HTMLURL:    github.Ptr("https://github.com/org/repo/actions/runs/123"),
		},
	}
	raw, _ := json.Marshal(wrPayload)
	event := makeEvent("WorkflowRunEvent", "ci-bot", raw)

	msg := eventToMessage(event, repo)

	if !strings.Contains(msg.Title, "Workflow CI failure") {
		t.Errorf("expected workflow name and conclusion in title, got %q", msg.Title)
	}
	if msg.Severity != "error" {
		t.Errorf("expected severity 'error' for failure, got %q", msg.Severity)
	}
}

func TestEventToMessage_CommonFields(t *testing.T) {
	repo := RepoConfig{Owner: "org", Name: "repo"}
	raw, _ := json.Marshal(github.PushEvent{Ref: github.Ptr("refs/heads/main")})
	event := makeEvent("PushEvent", "testuser", raw)

	msg := eventToMessage(event, repo)

	if msg.Author != "testuser" {
		t.Errorf("expected author 'testuser', got %q", msg.Author)
	}
	if msg.System != "homerun2-git-pitcher" {
		t.Errorf("expected system 'homerun2-git-pitcher', got %q", msg.System)
	}
	if !strings.Contains(msg.Tags, "github") {
		t.Errorf("expected 'github' in tags, got %q", msg.Tags)
	}
}

// makeEvent is a test helper that constructs a github.Event with a raw payload.
func makeEvent(eventType, login string, rawPayload []byte) *github.Event {
	now := time.Now()
	ts := github.Timestamp{Time: now}
	eventID := "test-event-id"
	rawMsg := json.RawMessage(rawPayload)
	return &github.Event{
		ID:         &eventID,
		Type:       &eventType,
		Actor:      &github.User{Login: &login},
		CreatedAt:  &ts,
		RawPayload: &rawMsg,
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
