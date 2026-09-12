package watcher

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/go-github/v87/github"
	"github.com/redis/go-redis/v9"
)

// Regression tests for #34: already-seen events must not be pitched again,
// neither when their dedup entry expires nor after a restart.

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func pushEventAt(id string, createdAt time.Time) *github.Event {
	ev := makeEvent("PushEvent", "octocat", []byte(`{"ref":"refs/heads/main","commits":[]}`))
	ev.ID = &id
	ev.CreatedAt = &github.Timestamp{Time: createdAt}
	return ev
}

func newTestWatcher(t *testing.T, dedup DedupStore, now func() time.Time) (*GitHubWatcher, RepoConfig) {
	t.Helper()
	cfg := &WatchConfig{
		GitHub: GitHubConfig{
			Token: "test-token",
			Repos: []RepoConfig{
				{Owner: "org", Name: "repo", Interval: 5 * time.Minute, Events: []EventKind{EventPush}},
			},
		},
	}
	w, err := NewGitHubWatcher(cfg, dedup)
	if err != nil {
		t.Fatalf("NewGitHubWatcher: %v", err)
	}
	w.now = now
	return w, cfg.GitHub.Repos[0]
}

// poll runs one poll over events and returns the IDs that were pitched.
func poll(w *GitHubWatcher, repo RepoConfig, events ...*github.Event) []string {
	msgs := make(chan PitchEvent, len(events))
	w.processEvents(context.Background(), repo, events, msgs)
	close(msgs)

	byTitle := map[string]string{}
	for _, ev := range events {
		byTitle[eventToMessage(ev, repo).Timestamp] = ev.GetID()
	}
	var ids []string
	for m := range msgs {
		ids = append(ids, byTitle[m.Message.Timestamp])
	}
	return ids
}

// The incident: events stay on GitHub's 30-event page for days. Once their
// entries had expired, the next new event re-pitched all of them.
func TestProcessEvents_ExpiredEntryIsNotRepitched(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 5, 20, 19, 0, 0, 0, time.UTC)}
	store, _ := NewMemoryDedupStore(DefaultDedupConfig(), "")
	store.now = clock.now
	w, repo := newTestWatcher(t, store, clock.now)

	e1 := pushEventAt("1", clock.t.Add(-3*time.Hour))
	e2 := pushEventAt("2", clock.t.Add(-2*time.Hour))
	e3 := pushEventAt("3", clock.t.Add(-1*time.Hour))
	if got := poll(w, repo, e3, e2, e1); len(got) != 0 {
		t.Fatalf("first run must not pitch, got %v", got)
	}

	// Six days later a new event arrives; the old ones are still listed.
	clock.t = clock.t.Add(6 * 24 * time.Hour)
	e4 := pushEventAt("4", clock.t.Add(-time.Minute))
	got := poll(w, repo, e4, e3, e2, e1)
	if len(got) != 1 || got[0] != "4" {
		t.Fatalf("expected only the new event 4 to be pitched, got %v", got)
	}

	if got := poll(w, repo, e4, e3, e2, e1); len(got) != 0 {
		t.Fatalf("expected nothing on an unchanged page, got %v", got)
	}
}

func TestProcessEvents_UnseenEventWithinRetentionIsPitched(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)}
	store, _ := NewMemoryDedupStore(DefaultDedupConfig(), "")
	store.now = clock.now
	w, repo := newTestWatcher(t, store, clock.now)

	poll(w, repo, pushEventAt("1", clock.t.Add(-time.Hour)))

	clock.t = clock.t.Add(2 * time.Hour)
	got := poll(w, repo,
		pushEventAt("3", clock.t.Add(-time.Minute)),
		pushEventAt("2", clock.t.Add(-23*time.Hour)), // unseen, but inside the window
		pushEventAt("1", clock.t.Add(-3*time.Hour)),
	)
	if len(got) != 2 {
		t.Fatalf("expected events 3 and 2 to be pitched, got %v", got)
	}
}

func newMiniredisClient(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return mr, client
}

func TestRedisDedupStore_RestartDoesNotRepitch(t *testing.T) {
	_, client := newMiniredisClient(t)
	ctx := context.Background()
	now := time.Now()
	repos := []string{"org/repo"}

	store1 := NewRedisDedupStore(ctx, client, "test:seen:", DefaultDedupConfig(), repos)
	w1, repo := newTestWatcher(t, store1, time.Now)
	e1 := pushEventAt("1", now.Add(-3*time.Hour))
	e2 := pushEventAt("2", now.Add(-2*time.Hour))
	poll(w1, repo, e2, e1)

	// Restart: a new process with an empty memory, the same Redis.
	store2 := NewRedisDedupStore(ctx, client, "test:seen:", DefaultDedupConfig(), repos)
	w2, repo := newTestWatcher(t, store2, time.Now)
	if w2.firstRun["org/repo"] {
		t.Fatal("expected no first-run suppression when Redis holds state")
	}

	// Event 3 was created while the pitcher was down; it is new and must go out.
	e3 := pushEventAt("3", now.Add(-10*time.Minute))
	got := poll(w2, repo, e3, e2, e1)
	if len(got) != 1 || got[0] != "3" {
		t.Fatalf("expected only event 3 after restart, got %v", got)
	}
}

func TestRedisDedupStore_TrimsAndExpires(t *testing.T) {
	mr, client := newMiniredisClient(t)
	cfg := DedupConfig{MaxEventsPerRepo: 2, Retention: 24 * time.Hour}
	s := NewRedisDedupStore(context.Background(), client, "test:seen:", cfg, []string{"org/repo"})
	now := time.Now()

	s.Mark("org/repo", "expired", now.Add(-25*time.Hour))
	s.Mark("org/repo", "a", now.Add(-3*time.Hour))
	s.Mark("org/repo", "b", now.Add(-2*time.Hour))
	s.Mark("org/repo", "c", now.Add(-1*time.Hour))

	members, err := mr.ZMembers("test:seen:org/repo")
	if err != nil {
		t.Fatalf("ZMembers: %v", err)
	}
	if len(members) != 2 || members[0] != "b" || members[1] != "c" {
		t.Fatalf("expected [b c] after trimming, got %v", members)
	}
	if ttl := mr.TTL("test:seen:org/repo"); ttl <= cfg.Retention {
		t.Fatalf("expected a TTL beyond the retention, got %s", ttl)
	}
}

func TestRedisDedupStore_RedisDownKeepsDeduplicatingInMemory(t *testing.T) {
	mr, client := newMiniredisClient(t)
	mr.Close()

	s := NewRedisDedupStore(context.Background(), client, "test:seen:", DefaultDedupConfig(), []string{"org/repo"})
	s.Mark("org/repo", "1", time.Now())
	if !s.Seen("org/repo", "1") {
		t.Fatal("expected the in-memory copy to remember event 1")
	}
}

func TestMemoryDedupStore_LoadsStateWrittenBeforeCreatedAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dedup-state.json")
	seenAt := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if err := os.WriteFile(path, []byte(`{"org/repo":[{"eventId":"e1","seenAt":"`+seenAt+`"}]}`), 0600); err != nil {
		t.Fatal(err)
	}

	s, _ := NewMemoryDedupStore(DefaultDedupConfig(), path)
	if !s.Seen("org/repo", "e1") {
		t.Fatal("expected an entry with only seenAt to load")
	}
}

func TestMemoryDedupStore_MarkTwiceKeepsOneEntry(t *testing.T) {
	s, _ := NewMemoryDedupStore(DefaultDedupConfig(), "")
	s.Mark("org/repo", "1", time.Now())
	s.Mark("org/repo", "1", time.Now())
	if n := s.Stats()["org/repo"]; n != 1 {
		t.Fatalf("expected 1 entry, got %d", n)
	}
}
