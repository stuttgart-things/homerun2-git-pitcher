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
		{"IssuesEvent", EventIssues},
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
	if msg.Tags != "github,issue_comment,org/repo,repo=org/repo,number=7,action=created,actor=reporter" {
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
	if msg.Tags != "github,issue_comment,org/repo,morning,daily,repo=org/repo,number=7,action=created,actor=reporter" {
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
		{"issue opened", issuesEvent("opened", "", "bug"), open, true},
		{"issue closed", issuesEvent("closed", "completed"), open, true},
		{"issue reopened", issuesEvent("reopened", ""), open, true},
		{"issue labeled", issuesEvent("labeled", ""), open, false},
		{"issue edited", issuesEvent("edited", ""), open, false},
		{"issue with label", issuesEvent("opened", "", "daily-pr-report"), filtered, true},
		{"issue without label", issuesEvent("opened", "", "bug"), filtered, false},
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

func issuesEvent(action, stateReason string, labels ...string) *github.Event {
	var ls []*github.Label
	for _, l := range labels {
		ls = append(ls, &github.Label{Name: github.Ptr(l)})
	}
	issue := &github.Issue{
		Number:  github.Ptr(12),
		Title:   github.Ptr("scout: messages endpoint"),
		HTMLURL: github.Ptr("https://github.com/org/repo/issues/12"),
		Labels:  ls,
	}
	if stateReason != "" {
		issue.StateReason = github.Ptr(stateReason)
	}
	raw, _ := json.Marshal(github.IssuesEvent{Action: github.Ptr(action), Issue: issue})
	return makeEvent("IssuesEvent", "alice", raw)
}

func TestEventToMessage_Issues(t *testing.T) {
	repo := RepoConfig{Owner: "org", Name: "repo"}

	tests := []struct {
		name        string
		action      string
		stateReason string
		body        string
		severity    string
	}{
		{"opened", "opened", "", "opened by alice", "info"},
		{"closed completed", "closed", "completed", "closed by alice (completed)", "success"},
		{"closed not planned", "closed", "not_planned", "closed by alice (not_planned)", "info"},
		{"reopened", "reopened", "", "reopened by alice", "warning"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := eventToMessage(issuesEvent(tt.action, tt.stateReason), repo)

			wantTitle := "Issue #12: scout: messages endpoint (" + tt.action + ") on org/repo"
			if msg.Title != wantTitle {
				t.Errorf("title = %q, want %q", msg.Title, wantTitle)
			}
			if msg.Message != tt.body {
				t.Errorf("body = %q, want %q", msg.Message, tt.body)
			}
			if msg.Severity != tt.severity {
				t.Errorf("severity = %q, want %q", msg.Severity, tt.severity)
			}
			if want := "github,issues,org/repo,repo=org/repo,number=12,action=" + tt.action + ",actor=alice"; msg.Tags != want {
				t.Errorf("tags = %q, want %q", msg.Tags, want)
			}
			if msg.URL != "https://github.com/org/repo/issues/12" {
				t.Errorf("url = %q", msg.URL)
			}
		})
	}
}

func TestEventToMessage_IssuesWithoutTitle(t *testing.T) {
	repo := RepoConfig{Owner: "org", Name: "repo"}
	event := makeEvent("IssuesEvent", "alice", []byte(`{"action":"opened","issue":{"number":3}}`))

	msg := eventToMessage(event, repo)

	if msg.Title != "Issue #3 (opened) on org/repo" {
		t.Errorf("title = %q", msg.Title)
	}
	if msg.URL != "https://github.com/org/repo/issues/3" {
		t.Errorf("url = %q", msg.URL)
	}
}

// prEvent builds a pull request event of action by actor, from user's
// branch feat/x into main.
func prEvent(action, actor string, merged bool, mergedBy string) *github.Event {
	pr := &github.PullRequest{
		Number:  github.Ptr(4404),
		Title:   github.Ptr("feat: route teams"),
		HTMLURL: github.Ptr("https://github.com/org/repo/pull/4404"),
		User:    &github.User{Login: github.Ptr("dev")},
		Merged:  github.Ptr(merged),
		Head:    &github.PullRequestBranch{Ref: github.Ptr("feat/x")},
		Base:    &github.PullRequestBranch{Ref: github.Ptr("main")},
	}
	if mergedBy != "" {
		pr.MergedBy = &github.User{Login: github.Ptr(mergedBy)}
	}
	raw, _ := json.Marshal(github.PullRequestEvent{Action: github.Ptr(action), PullRequest: pr})
	return makeEvent("PullRequestEvent", actor, raw)
}

// TestEventToMessage_PullRequestTags checks the key=value tags and url of
// pull request messages (#78), and that title, message and the plain tags
// stay as they were: filters match on tags_contain [pull_request] and
// message_contains ["PR merged by"].
func TestEventToMessage_PullRequestTags(t *testing.T) {
	repo := RepoConfig{Owner: "org", Name: "repo"}
	const plain = "github,pull_request,org/repo,"

	tests := []struct {
		name    string
		event   *github.Event
		title   string
		message string
		tags    string
	}{
		{
			"merged", prEvent("merged", "maintainer", true, ""),
			"PR #4404: feat: route teams (merged) on org/repo",
			"PR merged by maintainer: feat/x → main",
			plain + "repo=org/repo,number=4404,action=merged,head=feat/x,base=main,actor=maintainer,title=feat: route teams",
		},
		{
			"closed and merged", prEvent("closed", "bot", true, "maintainer"),
			"PR #4404: feat: route teams (closed) on org/repo",
			"PR merged by maintainer: feat/x → main",
			plain + "repo=org/repo,number=4404,action=merged,head=feat/x,base=main,actor=maintainer,title=feat: route teams",
		},
		{
			"closed", prEvent("closed", "maintainer", false, ""),
			"PR #4404: feat: route teams (closed) on org/repo",
			"PR closed by dev: feat/x → main",
			plain + "repo=org/repo,number=4404,action=closed,head=feat/x,base=main,actor=maintainer,title=feat: route teams",
		},
		{
			"opened", prEvent("opened", "dev", false, ""),
			"PR #4404: feat: route teams (opened) on org/repo",
			"PR opened by dev: feat/x → main",
			plain + "repo=org/repo,number=4404,action=opened,head=feat/x,base=main,actor=dev,title=feat: route teams",
		},
		{
			"reopened", prEvent("reopened", "dev", false, ""),
			"PR #4404: feat: route teams (reopened) on org/repo",
			"PR reopened by dev: feat/x → main",
			plain + "repo=org/repo,number=4404,action=reopened,head=feat/x,base=main,actor=dev,title=feat: route teams",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := eventToMessage(tt.event, repo)
			if msg.Title != tt.title {
				t.Errorf("title = %q, want %q", msg.Title, tt.title)
			}
			if msg.Message != tt.message {
				t.Errorf("message = %q, want %q", msg.Message, tt.message)
			}
			if msg.Tags != tt.tags {
				t.Errorf("tags = %q, want %q", msg.Tags, tt.tags)
			}
			if msg.URL != "https://github.com/org/repo/pull/4404" {
				t.Errorf("url = %q", msg.URL)
			}
		})
	}
}

// TestEventToMessage_PullRequestTitleTag checks the title= tag (#80): the
// bare PR title, trimmed, a comma as ";", left out when empty, while the
// message title keeps the comma.
func TestEventToMessage_PullRequestTitleTag(t *testing.T) {
	repo := RepoConfig{Owner: "org", Name: "repo"}
	const kv = "github,pull_request,org/repo,repo=org/repo,number=4404,action=opened,head=feat/x,base=main,actor=dev"

	tests := []struct {
		name  string
		title string
		msg   string
		tags  string
	}{
		{"plain", "feat: route teams", "PR #4404: feat: route teams (opened) on org/repo", kv + ",title=feat: route teams"},
		{"comma", "fix(a,b): one, two", "PR #4404: fix(a,b): one, two (opened) on org/repo", kv + ",title=fix(a;b): one; two"},
		{"whitespace", "  feat: pad  ", "PR #4404:   feat: pad   (opened) on org/repo", kv + ",title=feat: pad"},
		{"blank", "   ", "PR #4404:     (opened) on org/repo", kv},
		{"empty", "", "PR #4404 (opened) on org/repo", kv},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := prEvent("opened", "dev", false, "")
			var p github.PullRequestEvent
			if err := json.Unmarshal(*event.RawPayload, &p); err != nil {
				t.Fatal(err)
			}
			p.PullRequest.Title = github.Ptr(tt.title)
			raw, _ := json.Marshal(p)
			event = makeEvent("PullRequestEvent", "dev", raw)

			msg := eventToMessage(event, repo)
			if msg.Title != tt.msg {
				t.Errorf("title = %q, want %q", msg.Title, tt.msg)
			}
			if msg.Tags != tt.tags {
				t.Errorf("tags = %q, want %q", msg.Tags, tt.tags)
			}
		})
	}
}

func TestEventToMessage_OtherEventTags(t *testing.T) {
	repo := RepoConfig{Owner: "org", Name: "repo"}

	push, _ := json.Marshal(github.PushEvent{
		Ref:     github.Ptr("refs/heads/fix,comma"),
		Pusher:  &github.CommitAuthor{Name: github.Ptr("testuser")},
		Compare: github.Ptr("https://github.com/org/repo/compare/a...b"),
	})
	release, _ := json.Marshal(github.ReleaseEvent{
		Action:  github.Ptr("published"),
		Release: &github.RepositoryRelease{TagName: github.Ptr("v1.2.3")},
	})
	run, _ := json.Marshal(github.WorkflowRunEvent{
		WorkflowRun: &github.WorkflowRun{
			Name:       github.Ptr("CI"),
			Conclusion: github.Ptr("failure"),
			HeadBranch: github.Ptr("main"),
			HTMLURL:    github.Ptr("https://github.com/org/repo/actions/runs/123"),
		},
	})

	tests := []struct {
		name  string
		event *github.Event
		tags  string
		url   string
	}{
		{
			"push", makeEvent("PushEvent", "testuser", push),
			"github,push,org/repo,repo=org/repo,branch=fix_comma,actor=testuser",
			"https://github.com/org/repo/compare/a...b",
		},
		{
			"release", makeEvent("ReleaseEvent", "releaser", release),
			"github,release,org/repo,repo=org/repo,action=published,tag=v1.2.3,actor=releaser",
			"https://github.com/org/repo/releases/tag/v1.2.3",
		},
		{
			"workflow run", makeEvent("WorkflowRunEvent", "ci-bot", run),
			"github,workflow_run,org/repo,repo=org/repo,workflow=CI,conclusion=failure,branch=main,actor=ci-bot",
			"https://github.com/org/repo/actions/runs/123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := eventToMessage(tt.event, repo)
			if msg.Tags != tt.tags {
				t.Errorf("tags = %q, want %q", msg.Tags, tt.tags)
			}
			if msg.URL != tt.url {
				t.Errorf("url = %q, want %q", msg.URL, tt.url)
			}
		})
	}
}
