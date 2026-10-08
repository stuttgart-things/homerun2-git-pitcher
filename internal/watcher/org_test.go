package watcher

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-github/v87/github"
)

// Tests for #65: watching an organization's public event feed.

func TestLoadWatchConfigOrgs(t *testing.T) {
	yaml := `
github:
  token: tok
  orgs:
    - name: stuttgart-things
      events: [pull_request, release]
      exclude_actors: ["renovate[bot]"]
      exclude_repos: [docs]
  repos:
    - owner: stuttgart-things
      name: stuttgart-things
      events: [issue_comment]
`
	path := filepath.Join(t.TempDir(), "watch.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	cfg, err := LoadWatchConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	org := cfg.GitHub.Orgs[0]
	if org.Interval != 5*time.Minute {
		t.Errorf("expected default 5m interval, got %s", org.Interval)
	}
	if len(org.Events) != 2 {
		t.Errorf("expected 2 events, got %v", org.Events)
	}
	keys := cfg.DedupKeys()
	if len(keys) != 2 || keys[0] != "stuttgart-things/stuttgart-things" || keys[1] != "org:stuttgart-things" {
		t.Errorf("unexpected dedup keys: %v", keys)
	}
}

func TestLoadWatchConfigOrgsOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch.yaml")
	if err := os.WriteFile(path, []byte(`github: {token: tok, orgs: [{name: o}]}`), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
	cfg, err := LoadWatchConfig(path)
	if err != nil {
		t.Fatalf("an org without repos must be valid: %v", err)
	}
	if len(cfg.GitHub.Orgs[0].Events) != 4 {
		t.Errorf("expected 4 default events, got %v", cfg.GitHub.Orgs[0].Events)
	}
}

func TestLoadWatchConfigOrgValidation(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{"no name", `github: {token: tok, orgs: [{interval: 1m}]}`},
		{"bad interval", `github: {token: tok, orgs: [{name: o, interval: 5s}]}`},
		{"bad event", `github: {token: tok, orgs: [{name: o, events: [invalid]}]}`},
		{"nothing to watch", `github: {token: tok}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "watch.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0644); err != nil {
				t.Fatalf("failed to write test file: %v", err)
			}
			if _, err := LoadWatchConfig(path); err == nil {
				t.Error("expected validation error, got nil")
			}
		})
	}
}

// orgEvent is a public org event of repo by actor.
func orgEvent(id, repo, actor string) *github.Event {
	raw, _ := json.Marshal(github.ReleaseEvent{Release: &github.RepositoryRelease{
		TagName: github.Ptr("v1.0.0"),
		HTMLURL: github.Ptr("https://github.com/" + repo + "/releases/tag/v1.0.0"),
	}})
	ev := makeEvent("ReleaseEvent", actor, raw)
	ev.ID = github.Ptr(id)
	ev.Repo = &github.Repository{Name: github.Ptr(repo)}
	return ev
}

func TestOrgRepoFor(t *testing.T) {
	w := &GitHubWatcher{config: &WatchConfig{GitHub: GitHubConfig{
		Repos: []RepoConfig{{Owner: "org", Name: "private-ish"}},
	}}}
	org := OrgConfig{
		Name:          "org",
		Events:        []EventKind{EventRelease},
		Stream:        "org-stream",
		ExcludeActors: []string{"renovate[bot]"},
		ExcludeRepos:  []string{"docs", "org/attic"},
	}

	tests := []struct {
		name  string
		event *github.Event
		want  bool
	}{
		{"plain", orgEvent("1", "org/app", "dev"), true},
		{"excluded actor", orgEvent("2", "org/app", "Renovate[bot]"), false},
		{"excluded by name", orgEvent("3", "org/docs", "dev"), false},
		{"excluded by full name", orgEvent("4", "org/attic", "dev"), false},
		{"listed under repos", orgEvent("5", "org/private-ish", "dev"), false},
		{"no repo", makeEvent("ReleaseEvent", "dev", []byte(`{}`)), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, ok := w.orgRepoFor(org, tt.event)
			if ok != tt.want {
				t.Fatalf("orgRepoFor() ok = %v, want %v", ok, tt.want)
			}
			if ok && (repo.FullName() != "org/app" || repo.Stream != "org-stream" || !repo.WatchesEvent(EventRelease)) {
				t.Errorf("unexpected repo settings: %+v", repo)
			}
		})
	}
}

func TestFetchOrgAndSend(t *testing.T) {
	var feed []*github.Event
	w, _ := slimWatcher(t, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/orgs/org/events" {
			http.NotFound(rw, r)
			return
		}
		if got := r.URL.Query().Get("per_page"); got != "100" {
			t.Errorf("expected per_page=100, got %q", got)
		}
		_ = json.NewEncoder(rw).Encode(feed)
	}))
	org := OrgConfig{Name: "org", Events: []EventKind{EventRelease}, ExcludeActors: []string{"renovate[bot]"}}
	w.config.GitHub.Orgs = []OrgConfig{org}
	w.firstRun[org.DedupKey()] = true

	poll := func() []string {
		msgs := make(chan PitchEvent, 10)
		w.fetchOrgAndSend(context.Background(), org, msgs)
		close(msgs)
		var titles []string
		for m := range msgs {
			titles = append(titles, m.Message.Title)
		}
		return titles
	}

	// First poll only marks what is already there.
	feed = []*github.Event{orgEvent("1", "org/old", "dev")}
	if got := poll(); len(got) != 0 {
		t.Fatalf("first poll must not pitch, got %v", got)
	}

	feed = []*github.Event{
		orgEvent("3", "org/app", "dev"),
		orgEvent("2", "org/app", "renovate[bot]"),
		orgEvent("1", "org/old", "dev"),
	}
	got := poll()
	if len(got) != 1 || got[0] != "Release v1.0.0 on org/app" {
		t.Fatalf("expected one release of org/app, got %v", got)
	}

	if got := poll(); len(got) != 0 {
		t.Errorf("seen events must not be pitched again, got %v", got)
	}
}
