package watcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"time"

	"github.com/google/go-github/v87/github"
)

// The Events API lists an event 30s to 6h after it happened, which held
// back the daily PR report comments for hours (#71). A repo watching
// issue_comment therefore reads its comments from
// GET /repos/{owner}/{repo}/issues/comments, which has no such delay.

const (
	// commentsPerPage is the page size of the comments endpoint (max 100).
	commentsPerPage = 100
	// commentSinceOverlap re-reads this much before the newest updated_at
	// seen, so a comment updated in the same second is not lost. The dedup
	// store drops what was already pitched.
	commentSinceOverlap = time.Minute
	// issueCacheTTL is how long an issue's title and labels are reused.
	issueCacheTTL = time.Hour
)

// commentState is what the comment poller of one repo keeps between polls.
type commentState struct {
	// floor is the earliest creation time a comment may have to be
	// pitched: the start without persisted state, so no backlog is pitched.
	floor time.Time
	// since is the next ?since=: the newest updated_at seen.
	since  time.Time
	issues map[string]cachedIssue
}

type cachedIssue struct {
	issue   *github.Issue
	fetched time.Time
}

// commentDedupKey is the key under which the dedup store keeps the IDs of
// a repo's comments, apart from its event IDs.
func commentDedupKey(repo RepoConfig) string {
	return "comments:" + repo.FullName()
}

// commentStateFor returns the comment state of repo, creating it on the
// first poll. Without persisted dedup state polling starts now; with it,
// it reaches back the dedup retention, like the event feed after a restart.
func (w *GitHubWatcher) commentStateFor(repo RepoConfig) *commentState {
	w.mu.Lock()
	defer w.mu.Unlock()
	key := commentDedupKey(repo)
	if st, ok := w.comments[key]; ok {
		return st
	}
	now := w.now()
	floor := now
	if w.dedup.Stats()[key] > 0 {
		floor = now.Add(-w.dedup.Retention())
	}
	st := &commentState{floor: floor, since: floor, issues: map[string]cachedIssue{}}
	w.comments[key] = st
	slog.Info("comment poller starting", "repo", repo.FullName(), "since", floor.Format(time.RFC3339))
	return st
}

// fetchCommentsAndSend pitches the comments of repo created since the last
// poll. since filters by updated_at, so an edited comment comes back too:
// it is dropped by its ID, or by being older than the floor.
func (w *GitHubWatcher) fetchCommentsAndSend(ctx context.Context, repo RepoConfig, msgs chan<- PitchEvent) {
	logger := slog.With("repo", repo.FullName())
	key := commentDedupKey(repo)
	st := w.commentStateFor(repo)

	now := w.now()
	cutoff := st.floor
	if c := now.Add(-w.dedup.Retention()); c.After(cutoff) {
		cutoff = c
	}
	for k, c := range st.issues {
		if now.Sub(c.fetched) > issueCacheTTL {
			delete(st.issues, k)
		}
	}

	since := st.since.Add(-commentSinceOverlap)
	opts := &github.IssueListCommentsOptions{
		Sort:        github.Ptr("created"),
		Direction:   github.Ptr("asc"),
		Since:       &since,
		ListOptions: github.ListOptions{PerPage: commentsPerPage},
	}

	newest := st.since
	complete := true
	var newCount int
	for {
		comments, resp, err := w.client.Issues.ListComments(ctx, repo.Owner, repo.Name, 0, opts)
		w.updateRateLimit(resp)
		if err != nil {
			logger.Error("failed to list issue comments", "error", err)
			return
		}

		for _, c := range comments {
			if u := c.GetUpdatedAt().Time; u.After(newest) {
				newest = u
			}
			id := strconv.FormatInt(c.GetID(), 10)
			if w.dedup.Seen(key, id) {
				continue
			}
			createdAt := c.GetCreatedAt().Time
			if createdAt.Before(cutoff) {
				// Created before the start or the retention: an edit.
				continue
			}

			issue, err := w.issueFor(ctx, repo, st, c)
			if isGone(err) {
				// A deleted issue would hold since back for good.
				logger.Warn("issue of comment is gone, dropping comment", "comment", id, "error", err)
				w.dedup.Mark(key, id, createdAt)
				continue
			}
			if err != nil {
				// Retry on the next poll, from the same since.
				logger.Warn("failed to fetch issue of comment", "comment", id, "error", err)
				complete = false
				continue
			}

			event, err := commentEvent(c, issue)
			if err != nil {
				logger.Warn("failed to build event from comment", "comment", id, "error", err)
				w.dedup.Mark(key, id, createdAt)
				continue
			}
			if !wantsPayload(event, repo) {
				w.dedup.Mark(key, id, createdAt)
				continue
			}

			select {
			case msgs <- PitchEvent{Message: eventToMessage(event, repo), Stream: w.config.ResolveStream(repo)}:
				w.dedup.Mark(key, id, createdAt)
				newCount++
			case <-ctx.Done():
				return
			}
		}

		if resp == nil || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	if complete {
		st.since = newest
	}
	if newCount > 0 {
		logger.Info("new issue comments detected", "count", newCount)
	}
}

// issueFor returns the issue a comment belongs to, from the cache or from
// its issue_url: the comment carries neither the issue's title nor labels.
func (w *GitHubWatcher) issueFor(ctx context.Context, repo RepoConfig, st *commentState, c *github.IssueComment) (*github.Issue, error) {
	issueURL := c.GetIssueURL()
	if cached, ok := st.issues[issueURL]; ok {
		return cached.issue, nil
	}
	number, err := strconv.Atoi(path.Base(issueURL))
	if err != nil {
		return nil, fmt.Errorf("issue number from %q: %w", issueURL, err)
	}
	issue, resp, err := w.client.Issues.Get(ctx, repo.Owner, repo.Name, number)
	w.updateRateLimit(resp)
	if err != nil {
		return nil, err
	}
	st.issues[issueURL] = cachedIssue{issue: issue, fetched: w.now()}
	return issue, nil
}

// isGone reports whether err is GitHub's answer for a deleted issue.
func isGone(err error) bool {
	var ghErr *github.ErrorResponse
	if !errors.As(err, &ghErr) || ghErr.Response == nil {
		return false
	}
	return ghErr.Response.StatusCode == http.StatusNotFound || ghErr.Response.StatusCode == http.StatusGone
}

// commentEvent wraps a comment in the IssueCommentEvent the Events API
// would have listed, so wantsPayload and eventToMessage judge and format it
// exactly as before.
func commentEvent(c *github.IssueComment, issue *github.Issue) (*github.Event, error) {
	raw, err := json.Marshal(&github.IssueCommentEvent{
		Action:  github.Ptr("created"),
		Issue:   issue,
		Comment: c,
	})
	if err != nil {
		return nil, err
	}
	rawMsg := json.RawMessage(raw)
	return &github.Event{
		ID:         github.Ptr(strconv.FormatInt(c.GetID(), 10)),
		Type:       github.Ptr("IssueCommentEvent"),
		Actor:      c.User,
		CreatedAt:  c.CreatedAt,
		RawPayload: &rawMsg,
	}, nil
}
