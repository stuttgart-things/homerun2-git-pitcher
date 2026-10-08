package watcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/go-github/v87/github"
)

// Regression tests for #64: since 2025-10-07 the Events API sends slim
// payloads, and the watcher fetches what is missing.

// slimWatcher returns a watcher whose GitHub client talks to handler.
func slimWatcher(t *testing.T, handler http.Handler) (*GitHubWatcher, RepoConfig) {
	t.Helper()
	// WithEnterpriseURLs puts the API under /api/v3/.
	srv := httptest.NewServer(http.StripPrefix("/api/v3", handler))
	t.Cleanup(srv.Close)

	w, repo := newTestWatcher(t, nil, time.Now)
	client, err := github.NewClient(github.WithEnterpriseURLs(srv.URL+"/", srv.URL+"/"))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	w.client = client
	return w, repo
}

// The PR payload as the Events API sends it now: no title, user, html_url.
const slimPRPayload = `{"action":"merged","number":1353,"pull_request":{"id":1,"number":1353,
"url":"https://api.github.com/repos/org/repo/pulls/1353",
"base":{"ref":"main","sha":"aaa"},"head":{"ref":"feat/x","sha":"bbb"}}}`

func TestFillPayload_PullRequest(t *testing.T) {
	w, repo := slimWatcher(t, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/org/repo/pulls/1353" {
			http.NotFound(rw, r)
			return
		}
		_ = json.NewEncoder(rw).Encode(github.PullRequest{
			Number:   github.Ptr(1353),
			Title:    github.Ptr("feat: pin motd"),
			HTMLURL:  github.Ptr("https://github.com/org/repo/pull/1353"),
			User:     &github.User{Login: github.Ptr("dev")},
			MergedBy: &github.User{Login: github.Ptr("maintainer")},
			Head:     &github.PullRequestBranch{Ref: github.Ptr("feat/x")},
			Base:     &github.PullRequestBranch{Ref: github.Ptr("main")},
		})
	}))
	event := makeEvent("PullRequestEvent", "maintainer", []byte(slimPRPayload))

	w.fillPayload(context.Background(), event, repo)
	msg := eventToMessage(event, repo)

	if msg.Title != "PR #1353: feat: pin motd (merged) on org/repo" {
		t.Errorf("unexpected title: %q", msg.Title)
	}
	if msg.Severity != "success" {
		t.Errorf("expected severity 'success' for merged, got %q", msg.Severity)
	}
	if msg.Message != "PR merged by maintainer: feat/x → main" {
		t.Errorf("unexpected body: %q", msg.Message)
	}
	if msg.URL != "https://github.com/org/repo/pull/1353" {
		t.Errorf("unexpected url: %s", msg.URL)
	}
}

func TestFillPayload_PullRequestFetchFails(t *testing.T) {
	w, repo := slimWatcher(t, http.NotFoundHandler())
	event := makeEvent("PullRequestEvent", "maintainer", []byte(slimPRPayload))

	w.fillPayload(context.Background(), event, repo)
	msg := eventToMessage(event, repo)

	if msg.Title != "PR #1353 (merged) on org/repo" {
		t.Errorf("unexpected fallback title: %q", msg.Title)
	}
	if msg.Message != "PR merged by maintainer: feat/x → main" {
		t.Errorf("expected the actor as merger, got %q", msg.Message)
	}
	if msg.URL != "https://github.com/org/repo/pull/1353" {
		t.Errorf("expected url built from repo and number, got %s", msg.URL)
	}
}

func TestFillPayload_Push(t *testing.T) {
	w, repo := slimWatcher(t, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/org/repo/compare/aaa...bbb" {
			http.NotFound(rw, r)
			return
		}
		_ = json.NewEncoder(rw).Encode(github.CommitsComparison{
			HTMLURL: github.Ptr("https://github.com/org/repo/compare/aaa...bbb"),
			Commits: []*github.RepositoryCommit{
				{SHA: github.Ptr("c1"), Commit: &github.Commit{Message: github.Ptr("fix: one")}},
				{SHA: github.Ptr("bbb"), Commit: &github.Commit{Message: github.Ptr("fix: two")}},
			},
		})
	}))
	event := makeEvent("PushEvent", "octocat",
		[]byte(`{"push_id":1,"repository_id":2,"ref":"refs/heads/main","before":"aaa","head":"bbb"}`))

	w.fillPayload(context.Background(), event, repo)
	msg := eventToMessage(event, repo)

	if msg.Message != "2 commit(s) pushed by octocat: fix: two" {
		t.Errorf("unexpected body: %q", msg.Message)
	}
	if msg.URL != "https://github.com/org/repo/compare/aaa...bbb" {
		t.Errorf("unexpected url: %s", msg.URL)
	}
}

func TestFillPayload_FullPayloadUntouched(t *testing.T) {
	w, repo := slimWatcher(t, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected API call: %s", r.URL.Path)
	}))
	raw, _ := json.Marshal(github.PullRequestEvent{
		Action:      github.Ptr("opened"),
		PullRequest: &github.PullRequest{Number: github.Ptr(1), Title: github.Ptr("full")},
	})
	event := makeEvent("PullRequestEvent", "dev", raw)

	w.fillPayload(context.Background(), event, repo)

	if got := eventToMessage(event, repo).Title; got != "PR #1: full (opened) on org/repo" {
		t.Errorf("unexpected title: %q", got)
	}
}
