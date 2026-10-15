package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-github/v87/github"
)

// Tests for #71: issue comments are read from the comments endpoint, not
// from the delayed event feed.

var commentT0 = time.Date(2026, 10, 14, 4, 0, 0, 0, time.UTC)

// fakeComments serves /repos/org/repo/issues/comments in pages of pageSize
// and /repos/org/repo/issues/{n} from issues.
type fakeComments struct {
	t         *testing.T
	mu        sync.Mutex
	comments  []*github.IssueComment
	issues    map[int]*github.Issue
	pageSize  int
	failIssue bool

	listQueries []string
	issueGets   map[int]int
}

func (f *fakeComments) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Path == "/repos/org/repo/issues/comments":
		f.listQueries = append(f.listQueries, r.URL.RawQuery)
		q := r.URL.Query()
		if q.Get("sort") != "created" || q.Get("direction") != "asc" || q.Get("since") == "" {
			f.t.Errorf("unexpected comments query: %s", r.URL.RawQuery)
		}
		since, err := time.Parse(time.RFC3339, q.Get("since"))
		if err != nil {
			f.t.Errorf("bad since %q: %v", q.Get("since"), err)
		}
		var match []*github.IssueComment
		for _, c := range f.comments {
			if !c.GetUpdatedAt().Before(since) {
				match = append(match, c)
			}
		}
		size := f.pageSize
		if size == 0 {
			size = 100
		}
		page := 1
		_, _ = fmt.Sscanf(q.Get("page"), "%d", &page)
		start := (page - 1) * size
		end := start + size
		if start > len(match) {
			start = len(match)
		}
		if end > len(match) {
			end = len(match)
		} else if end < len(match) {
			rw.Header().Set("Link", fmt.Sprintf(`<http://%s%s?page=%d>; rel="next"`, r.Host, r.URL.Path, page+1))
		}
		_ = json.NewEncoder(rw).Encode(match[start:end])

	case strings.HasPrefix(r.URL.Path, "/repos/org/repo/issues/"):
		var n int
		_, _ = fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/repos/org/repo/issues/"), "%d", &n)
		f.issueGets[n]++
		issue, ok := f.issues[n]
		if f.failIssue {
			http.Error(rw, "boom", http.StatusInternalServerError)
			return
		}
		if !ok {
			http.NotFound(rw, r)
			return
		}
		_ = json.NewEncoder(rw).Encode(issue)

	default:
		http.NotFound(rw, r)
	}
}

func (f *fakeComments) add(c *github.IssueComment) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.comments = append(f.comments, c)
}

func makeComment(id int64, issue int, body string, created, updated time.Time) *github.IssueComment {
	return &github.IssueComment{
		ID:        github.Ptr(id),
		Body:      github.Ptr(body),
		User:      &github.User{Login: github.Ptr("reporter")},
		HTMLURL:   github.Ptr(fmt.Sprintf("https://github.com/org/repo/issues/%d#issuecomment-%d", issue, id)),
		IssueURL:  github.Ptr(fmt.Sprintf("https://api.github.com/repos/org/repo/issues/%d", issue)),
		CreatedAt: &github.Timestamp{Time: created},
		UpdatedAt: &github.Timestamp{Time: updated},
	}
}

func makeIssue(n int, title string, labels ...string) *github.Issue {
	issue := &github.Issue{Number: github.Ptr(n), Title: github.Ptr(title)}
	for _, l := range labels {
		issue.Labels = append(issue.Labels, &github.Label{Name: github.Ptr(l)})
	}
	return issue
}

// commentWatcher returns a watcher polling org/repo for labelled comments
// against a fake GitHub, started at commentT0.
func commentWatcher(t *testing.T) (*GitHubWatcher, RepoConfig, *fakeComments, *fakeClock) {
	t.Helper()
	fake := &fakeComments{
		t: t,
		issues: map[int]*github.Issue{
			7: makeIssue(7, "Daily PR Report – KW 41/2026", "daily-pr-report"),
			8: makeIssue(8, "Some bug", "bug"),
		},
		issueGets: map[int]int{},
	}
	w, _ := slimWatcher(t, fake)
	clock := &fakeClock{t: commentT0}
	w.now = clock.now
	// The dedup store prunes by its own clock: with the real one, every
	// entry around commentT0 expired once commentT0 was 24h in the past.
	s, ok := w.dedup.(*MemoryDedupStore)
	if !ok {
		t.Fatalf("expected a MemoryDedupStore, got %T", w.dedup)
	}
	s.now = clock.now
	repo := RepoConfig{
		Owner:    "org",
		Name:     "repo",
		Interval: 5 * time.Minute,
		Events:   []EventKind{EventIssueComment},
		Labels:   []string{"daily-pr-report"},
	}
	return w, repo, fake, clock
}

// pollComments runs one comment poll and returns the pitched messages.
func pollComments(w *GitHubWatcher, repo RepoConfig) []PitchEvent {
	msgs := make(chan PitchEvent, 100)
	w.fetchCommentsAndSend(context.Background(), repo, msgs)
	close(msgs)
	var out []PitchEvent
	for m := range msgs {
		out = append(out, m)
	}
	return out
}

func urls(events []PitchEvent) []string {
	var out []string
	for _, e := range events {
		out = append(out, e.Message.URL)
	}
	return out
}

func TestFetchComments_NoBacklogAtStart(t *testing.T) {
	w, repo, fake, _ := commentWatcher(t)
	fake.add(makeComment(1, 7, "yesterday", commentT0.Add(-time.Hour), commentT0.Add(-time.Hour)))

	if got := pollComments(w, repo); len(got) != 0 {
		t.Fatalf("comments from before the start must not be pitched, got %v", urls(got))
	}
}

func TestFetchComments_LabelFilter(t *testing.T) {
	w, repo, fake, clock := commentWatcher(t)
	pollComments(w, repo)

	clock.t = commentT0.Add(5 * time.Minute)
	fake.add(makeComment(10, 7, "report", commentT0.Add(time.Minute), commentT0.Add(time.Minute)))
	fake.add(makeComment(11, 8, "bug talk", commentT0.Add(2*time.Minute), commentT0.Add(2*time.Minute)))

	got := pollComments(w, repo)
	if len(got) != 1 || got[0].Message.URL != "https://github.com/org/repo/issues/7#issuecomment-10" {
		t.Fatalf("expected only the comment on the labelled issue, got %v", urls(got))
	}
	if !w.dedup.Seen(commentDedupKey(repo), "11") {
		t.Error("a filtered comment must be marked seen so it is not judged again")
	}
}

func TestFetchComments_MessageUnchanged(t *testing.T) {
	w, repo, fake, clock := commentWatcher(t)
	pollComments(w, repo)

	body := "<!-- homerun2:severity=warning -->\n<!-- homerun2:tags=morning,daily -->\n## Morning report\n" + strings.Repeat("ä", maxCommentLen)
	created := commentT0.Add(time.Minute)
	fake.add(makeComment(10, 7, body, created, created))
	clock.t = commentT0.Add(5 * time.Minute)

	got := pollComments(w, repo)
	if len(got) != 1 {
		t.Fatalf("expected one message, got %d", len(got))
	}
	msg := got[0].Message
	if msg.Title != "Comment on #7: Daily PR Report – KW 41/2026 on org/repo" {
		t.Errorf("unexpected title: %q", msg.Title)
	}
	if msg.Severity != "warning" {
		t.Errorf("expected severity from marker, got %q", msg.Severity)
	}
	if msg.Tags != "github,issue_comment,org/repo,morning,daily" {
		t.Errorf("unexpected tags: %q", msg.Tags)
	}
	wantBody := string([]rune("## Morning report\n" + strings.Repeat("ä", maxCommentLen))[:maxCommentLen]) + "..."
	if msg.Message != wantBody {
		t.Errorf("expected markers stripped and text cut at %d characters", maxCommentLen)
	}
	if msg.Author != "reporter" || msg.Timestamp != created.Format(time.RFC3339) || msg.System != "homerun2-git-pitcher" {
		t.Errorf("unexpected author/timestamp/system: %q %q %q", msg.Author, msg.Timestamp, msg.System)
	}
	if msg.URL != "https://github.com/org/repo/issues/7#issuecomment-10" {
		t.Errorf("unexpected url: %s", msg.URL)
	}

	// The same as the event feed's IssueCommentEvent made of it.
	payload := &github.IssueCommentEvent{
		Action:  github.Ptr("created"),
		Issue:   fake.issues[7],
		Comment: fake.comments[0],
	}
	raw, _ := json.Marshal(payload)
	ev := makeEvent("IssueCommentEvent", "reporter", raw)
	ev.CreatedAt = &github.Timestamp{Time: created}
	if want := eventToMessage(ev, repo); msg != want {
		t.Errorf("message differs from the event feed's:\n got %+v\nwant %+v", msg, want)
	}
}

func TestFetchComments_Dedup(t *testing.T) {
	w, repo, fake, clock := commentWatcher(t)
	pollComments(w, repo)

	fake.add(makeComment(10, 7, "report", commentT0.Add(time.Minute), commentT0.Add(time.Minute)))
	clock.t = commentT0.Add(5 * time.Minute)
	if got := pollComments(w, repo); len(got) != 1 {
		t.Fatalf("expected one message, got %v", urls(got))
	}

	// The since overlap lists it again.
	clock.t = commentT0.Add(10 * time.Minute)
	if got := pollComments(w, repo); len(got) != 0 {
		t.Errorf("a seen comment must not be pitched again, got %v", urls(got))
	}
	if n := fake.issueGets[7]; n != 1 {
		t.Errorf("expected the issue to be fetched once and cached, got %d fetches", n)
	}
}

func TestFetchComments_EditedNotRepitched(t *testing.T) {
	w, repo, fake, clock := commentWatcher(t)
	// Created before the start, edited after it.
	old := makeComment(1, 7, "old report", commentT0.Add(-time.Hour), commentT0.Add(-time.Hour))
	fake.add(old)
	pollComments(w, repo)

	fake.add(makeComment(10, 7, "report", commentT0.Add(time.Minute), commentT0.Add(time.Minute)))
	clock.t = commentT0.Add(5 * time.Minute)
	if got := pollComments(w, repo); len(got) != 1 {
		t.Fatalf("expected the new comment, got %v", urls(got))
	}

	fake.mu.Lock()
	old.UpdatedAt = &github.Timestamp{Time: commentT0.Add(6 * time.Minute)}
	fake.comments[1].UpdatedAt = &github.Timestamp{Time: commentT0.Add(7 * time.Minute)}
	fake.comments[1].Body = github.Ptr("report, edited")
	fake.mu.Unlock()

	clock.t = commentT0.Add(10 * time.Minute)
	if got := pollComments(w, repo); len(got) != 0 {
		t.Errorf("edited comments must not be pitched, got %v", urls(got))
	}

	// Two days on, the dedup entry of comment 10 has expired; an edit still
	// must not pitch it.
	fake.mu.Lock()
	fake.comments[1].UpdatedAt = &github.Timestamp{Time: commentT0.Add(48 * time.Hour)}
	fake.mu.Unlock()
	clock.t = commentT0.Add(48*time.Hour + time.Minute)
	if got := pollComments(w, repo); len(got) != 0 {
		t.Errorf("an edit after the dedup retention must not be pitched, got %v", urls(got))
	}
}

func TestFetchComments_Pagination(t *testing.T) {
	w, repo, fake, clock := commentWatcher(t)
	fake.pageSize = 2
	pollComments(w, repo)

	for i := range 5 {
		at := commentT0.Add(time.Duration(i+1) * time.Minute)
		fake.add(makeComment(int64(10+i), 7, fmt.Sprintf("report %d", i), at, at))
	}
	clock.t = commentT0.Add(10 * time.Minute)
	fake.listQueries = nil

	got := pollComments(w, repo)
	if len(got) != 5 {
		t.Fatalf("expected all 5 comments over 3 pages, got %v", urls(got))
	}
	for i, m := range got {
		if want := fmt.Sprintf("report %d", i); m.Message.Message != want {
			t.Errorf("message %d: expected %q in creation order, got %q", i, want, m.Message.Message)
		}
	}
	if len(fake.listQueries) != 3 {
		t.Errorf("expected 3 page requests, got %v", fake.listQueries)
	}
	if !strings.Contains(fake.listQueries[0], "per_page=100") || !strings.Contains(fake.listQueries[2], "page=3") {
		t.Errorf("unexpected page queries: %v", fake.listQueries)
	}
}

func TestFetchComments_SinceAdvances(t *testing.T) {
	w, repo, fake, clock := commentWatcher(t)
	pollComments(w, repo)

	at := commentT0.Add(3 * time.Minute)
	fake.add(makeComment(10, 7, "report", at, at))
	clock.t = commentT0.Add(5 * time.Minute)
	pollComments(w, repo)
	fake.listQueries = nil
	pollComments(w, repo)

	want := at.Add(-commentSinceOverlap).Format(time.RFC3339)
	if len(fake.listQueries) != 1 || !strings.Contains(fake.listQueries[0], "since="+strings.ReplaceAll(want, ":", "%3A")) {
		t.Errorf("expected since=%s (newest updated_at minus overlap), got %v", want, fake.listQueries)
	}
}

func TestFetchComments_IssueFetchFailsIsRetried(t *testing.T) {
	w, repo, fake, clock := commentWatcher(t)
	pollComments(w, repo)

	fake.add(makeComment(10, 7, "report", commentT0.Add(time.Minute), commentT0.Add(time.Minute)))
	fake.failIssue = true
	clock.t = commentT0.Add(5 * time.Minute)
	if got := pollComments(w, repo); len(got) != 0 {
		t.Fatalf("without its issue a comment cannot be judged, got %v", urls(got))
	}

	fake.failIssue = false
	clock.t = commentT0.Add(10 * time.Minute)
	if got := pollComments(w, repo); len(got) != 1 {
		t.Errorf("expected the comment on the retry, got %v", urls(got))
	}
}

func TestFetchComments_DeletedIssueDoesNotStall(t *testing.T) {
	w, repo, fake, clock := commentWatcher(t)
	pollComments(w, repo)

	// Issue 99 is not served: 404.
	fake.add(makeComment(10, 99, "on a deleted issue", commentT0.Add(time.Minute), commentT0.Add(time.Minute)))
	fake.add(makeComment(11, 7, "report", commentT0.Add(2*time.Minute), commentT0.Add(2*time.Minute)))
	clock.t = commentT0.Add(5 * time.Minute)
	if got := pollComments(w, repo); len(got) != 1 {
		t.Fatalf("expected the comment on the existing issue, got %v", urls(got))
	}
	if !w.commentStateFor(repo).since.Equal(commentT0.Add(2 * time.Minute)) {
		t.Errorf("a deleted issue must not hold since back, got %s", w.commentStateFor(repo).since)
	}
}

func TestFetchComments_PersistedStateReachesBack(t *testing.T) {
	w, repo, fake, _ := commentWatcher(t)
	// A restart with dedup state: comment 1 was pitched before, comment 2
	// was created while the pitcher was down.
	w.dedup.Mark(commentDedupKey(repo), "1", commentT0.Add(-2*time.Hour))
	fake.add(makeComment(1, 7, "pitched", commentT0.Add(-2*time.Hour), commentT0.Add(-2*time.Hour)))
	fake.add(makeComment(2, 7, "missed", commentT0.Add(-time.Hour), commentT0.Add(-time.Hour)))

	got := pollComments(w, repo)
	if len(got) != 1 || got[0].Message.Message != "missed" {
		t.Errorf("expected only the comment made while down, got %v", urls(got))
	}
}

func TestProcessEvents_DropsIssueComments(t *testing.T) {
	w, repo, _, _ := commentWatcher(t)
	w.firstRun[repo.FullName()] = false

	ev := issueCommentEvent("created", "report", "daily-pr-report")
	ev.ID = github.Ptr("42")
	msgs := make(chan PitchEvent, 1)
	w.processEvents(context.Background(), repo, []*github.Event{ev}, msgs)
	close(msgs)
	if len(msgs) != 0 {
		t.Error("issue comments from the event feed must not be pitched: the comment poller does")
	}
}

func TestPollRepoOnce_CommentsOnlySkipsEventFeed(t *testing.T) {
	w, repo, fake, _ := commentWatcher(t)
	var feedCalls int
	inner := fake
	w2, _ := slimWatcher(t, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/org/repo/events" {
			feedCalls++
			_, _ = rw.Write([]byte("[]"))
			return
		}
		inner.ServeHTTP(rw, r)
	}))
	w.client = w2.client

	msgs := make(chan PitchEvent, 10)
	w.pollRepoOnce(context.Background(), repo, msgs)
	if feedCalls != 0 {
		t.Errorf("a repo watching only issue_comment must not poll the event feed, got %d calls", feedCalls)
	}

	repo.Events = []EventKind{EventIssueComment, EventRelease}
	w.pollRepoOnce(context.Background(), repo, msgs)
	if feedCalls != 1 {
		t.Errorf("other event kinds must still use the event feed, got %d calls", feedCalls)
	}
	if len(fake.listQueries) != 2 {
		t.Errorf("expected the comments endpoint on every poll, got %d calls", len(fake.listQueries))
	}
}
