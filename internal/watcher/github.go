package watcher

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/go-github/v68/github"
	homerun "github.com/stuttgart-things/homerun-library/v3"
)

// GitHubWatcher implements GitWatcher by polling the GitHub API.
type GitHubWatcher struct {
	client *github.Client
	config *WatchConfig

	// lastSeen tracks the most recent event timestamp per repo to avoid duplicates.
	mu       sync.Mutex
	lastSeen map[string]time.Time
}

// NewGitHubWatcher creates a watcher from the given config.
func NewGitHubWatcher(cfg *WatchConfig) *GitHubWatcher {
	client := github.NewClient(nil).WithAuthToken(cfg.GitHub.Token)

	return &GitHubWatcher{
		client:   client,
		config:   cfg,
		lastSeen: make(map[string]time.Time),
	}
}

// Watch starts a goroutine per configured repo that polls for events at the
// configured interval. Events are converted to homerun Messages and sent on
// the returned channel. Polling stops when ctx is cancelled.
func (w *GitHubWatcher) Watch(ctx context.Context) (<-chan homerun.Message, error) {
	msgs := make(chan homerun.Message, 100)

	var wg sync.WaitGroup
	for _, repo := range w.config.GitHub.Repos {
		wg.Add(1)
		go func(repo RepoConfig) {
			defer wg.Done()
			w.pollRepo(ctx, repo, msgs)
		}(repo)
	}

	go func() {
		wg.Wait()
		close(msgs)
	}()

	return msgs, nil
}

// pollRepo polls a single repo at its configured interval.
func (w *GitHubWatcher) pollRepo(ctx context.Context, repo RepoConfig, msgs chan<- homerun.Message) {
	logger := slog.With("repo", repo.FullName())
	logger.Info("starting watcher", "interval", repo.Interval, "events", repo.Events)

	// Do an initial poll immediately.
	w.fetchAndSend(ctx, repo, msgs)

	ticker := time.NewTicker(repo.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("watcher stopped")
			return
		case <-ticker.C:
			w.fetchAndSend(ctx, repo, msgs)
		}
	}
}

// fetchAndSend fetches recent events from GitHub and sends new ones as messages.
func (w *GitHubWatcher) fetchAndSend(ctx context.Context, repo RepoConfig, msgs chan<- homerun.Message) {
	logger := slog.With("repo", repo.FullName())

	events, resp, err := w.client.Activity.ListRepositoryEvents(ctx, repo.Owner, repo.Name, &github.ListOptions{
		PerPage: 30,
	})
	if err != nil {
		logger.Error("failed to list events", "error", err)
		return
	}

	// Log rate limit usage.
	if resp.Rate.Limit > 0 {
		logger.Debug("rate limit",
			"remaining", resp.Rate.Remaining,
			"limit", resp.Rate.Limit,
			"reset", resp.Rate.Reset.Format(time.RFC3339),
		)
	}

	// Handle conditional requests (304 Not Modified).
	if resp.StatusCode == 304 {
		logger.Debug("no new events (304)")
		return
	}

	w.mu.Lock()
	cutoff := w.lastSeen[repo.FullName()]
	var newest time.Time
	w.mu.Unlock()

	var newCount int
	for _, event := range events {
		ts := event.GetCreatedAt().Time
		if !ts.After(cutoff) {
			continue
		}
		if ts.After(newest) {
			newest = ts
		}

		kind := eventTypeToKind(event.GetType())
		if kind == "" || !repo.WatchesEvent(kind) {
			continue
		}

		msg := eventToMessage(event, repo)
		select {
		case msgs <- msg:
			newCount++
		case <-ctx.Done():
			return
		}
	}

	if newest.After(cutoff) {
		w.mu.Lock()
		w.lastSeen[repo.FullName()] = newest
		w.mu.Unlock()
	}

	if newCount > 0 {
		logger.Info("new events detected", "count", newCount)
	}
}

// eventTypeToKind maps GitHub event type strings to EventKind.
func eventTypeToKind(ghType string) EventKind {
	switch ghType {
	case "PushEvent":
		return EventPush
	case "PullRequestEvent":
		return EventPullRequest
	case "ReleaseEvent":
		return EventRelease
	case "WorkflowRunEvent":
		return EventWorkflowRun
	default:
		return ""
	}
}

// eventToMessage converts a GitHub event into a homerun Message.
func eventToMessage(event *github.Event, repo RepoConfig) homerun.Message {
	kind := eventTypeToKind(event.GetType())
	actor := ""
	if event.GetActor() != nil {
		actor = event.GetActor().GetLogin()
	}

	title := fmt.Sprintf("[%s] %s on %s", kind, event.GetType(), repo.FullName())
	body := fmt.Sprintf("Event %s by %s at %s",
		event.GetID(),
		actor,
		event.GetCreatedAt().Format(time.RFC3339),
	)

	return homerun.Message{
		Title:     title,
		Message:   body,
		Severity:  "info",
		Author:    actor,
		Timestamp: event.GetCreatedAt().Format(time.RFC3339),
		System:    "homerun2-git-pitcher",
		Tags:      fmt.Sprintf("github,%s,%s", kind, repo.FullName()),
		Url:       fmt.Sprintf("https://github.com/%s", repo.FullName()),
	}
}
