package watcher

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v87/github"
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
		{"IssueCommentEvent", EventIssueComment},
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
	if msg.URL != "https://github.com/org/repo/compare/abc...def" {
		t.Errorf("unexpected url: %s", msg.URL)
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
	if msg.URL != "https://github.com/org/repo/pull/42" {
		t.Errorf("unexpected url: %s", msg.URL)
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
func issueCommentEvent(action, body string, labels ...string) *github.Event {
	var ls []*github.Label
	for _, l := range labels {
		ls = append(ls, &github.Label{Name: github.Ptr(l)})
	}
	payload := github.IssueCommentEvent{
		Action: github.Ptr(action),
		Issue: &github.Issue{
			Number: github.Ptr(7),
			Title:  github.Ptr("Daily PR Report – KW 41/2026"),
			Labels: ls,
		},
		Comment: &github.IssueComment{
			Body:    github.Ptr(body),
			HTMLURL: github.Ptr("https://github.com/org/repo/issues/7#issuecomment-1"),
		},
	}
	raw, _ := json.Marshal(payload)
	return makeEvent("IssueCommentEvent", "reporter", raw)
}

func TestEventToMessage_IssueComment(t *testing.T) {
	repo := RepoConfig{Owner: "org", Name: "repo"}

	msg := eventToMessage(issueCommentEvent("created", "5 merged, 1 open"), repo)

	if msg.Title != "Comment on #7: Daily PR Report – KW 41/2026 on org/repo" {
		t.Errorf("unexpected title: %q", msg.Title)
	}
	if msg.Message != "5 merged, 1 open" {
		t.Errorf("unexpected body: %q", msg.Message)
	}
	if msg.Severity != "info" {
		t.Errorf("expected severity 'info' without marker, got %q", msg.Severity)
	}
	if msg.Tags != "github,issue_comment,org/repo" {
		t.Errorf("unexpected tags: %q", msg.Tags)
	}
	if msg.URL != "https://github.com/org/repo/issues/7#issuecomment-1" {
		t.Errorf("unexpected url: %s", msg.URL)
	}
}

func TestEventToMessage_IssueCommentMarkers(t *testing.T) {
	repo := RepoConfig{Owner: "org", Name: "repo"}
	body := "<!-- homerun2:severity=warning -->\n<!-- homerun2:tags=morning,daily -->\n## Morning report\n#3520 checks red"

	msg := eventToMessage(issueCommentEvent("created", body), repo)

	if msg.Severity != "warning" {
		t.Errorf("expected severity 'warning' from marker, got %q", msg.Severity)
	}
	if msg.Tags != "github,issue_comment,org/repo,morning,daily" {
		t.Errorf("expected marker tags appended, got %q", msg.Tags)
	}
	if msg.Message != "## Morning report\n#3520 checks red" {
		t.Errorf("expected markers stripped, got %q", msg.Message)
	}
}

func TestEventToMessage_IssueCommentTruncation(t *testing.T) {
	repo := RepoConfig{Owner: "org", Name: "repo"}
	body := strings.Repeat("ä", maxCommentLen+10)

	msg := eventToMessage(issueCommentEvent("created", body), repo)

	want := strings.Repeat("ä", maxCommentLen) + "..."
	if msg.Message != want {
		t.Errorf("expected truncation to %d characters, got %d bytes", maxCommentLen, len(msg.Message))
	}
}

func TestWantsPayload(t *testing.T) {
	filtered := RepoConfig{Owner: "org", Name: "repo", Labels: []string{"daily-pr-report"}}
	open := RepoConfig{Owner: "org", Name: "repo"}

	tests := []struct {
		name  string
		event *github.Event
		repo  RepoConfig
		want  bool
	}{
		{"labelled issue", issueCommentEvent("created", "x", "bug", "daily-pr-report"), filtered, true},
		{"other label", issueCommentEvent("created", "x", "bug"), filtered, false},
		{"no label", issueCommentEvent("created", "x"), filtered, false},
		{"no filter", issueCommentEvent("created", "x"), open, true},
		{"edited", issueCommentEvent("edited", "x", "daily-pr-report"), filtered, false},
		{"not a comment", makeEvent("ReleaseEvent", "r", []byte(`{}`)), filtered, true},
		{"pr opened", makeEvent("PullRequestEvent", "r", []byte(`{"action":"opened","pull_request":{"number":1}}`)), open, true},
		{"pr merged", makeEvent("PullRequestEvent", "r", []byte(`{"action":"merged","pull_request":{"number":1}}`)), open, true},
		{"pr closed", makeEvent("PullRequestEvent", "r", []byte(`{"action":"closed","pull_request":{"number":1}}`)), open, true},
		{"pr labeled", makeEvent("PullRequestEvent", "r", []byte(`{"action":"labeled","pull_request":{"number":1}}`)), open, false},
		{"pr assigned", makeEvent("PullRequestEvent", "r", []byte(`{"action":"assigned","pull_request":{"number":1}}`)), open, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := wantsPayload(tt.event, tt.repo); got != tt.want {
				t.Errorf("wantsPayload() = %v, want %v", got, tt.want)
			}
		})
	}
}

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

	w, err := NewGitHubWatcher(cfg, nil)
	if err != nil {
		t.Fatalf("NewGitHubWatcher: %v", err)
	}

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
	dedup.Mark("org/repo", "existing-event", time.Now())

	w, err := NewGitHubWatcher(cfg, dedup)
	if err != nil {
		t.Fatalf("NewGitHubWatcher: %v", err)
	}

	// firstRun should be false since dedup has state for this repo.
	if w.firstRun["org/repo"] {
		t.Error("expected firstRun to be false when dedup has persisted state")
	}
}
