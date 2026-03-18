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
	dedup  DedupStore

	// firstRun tracks whether a repo has completed its initial poll.
	// On the first poll we mark events as seen without pitching them.
	mu       sync.Mutex
	firstRun map[string]bool
}

// NewGitHubWatcher creates a watcher from the given config.
// If dedup is nil, a default in-memory store is used.
func NewGitHubWatcher(cfg *WatchConfig, dedup DedupStore) *GitHubWatcher {
	client := github.NewClient(nil).WithAuthToken(cfg.GitHub.Token)

	if dedup == nil {
		dedup, _ = NewMemoryDedupStore(DefaultDedupConfig(), "")
	}

	firstRun := make(map[string]bool, len(cfg.GitHub.Repos))
	for _, repo := range cfg.GitHub.Repos {
		// If the dedup store already has state for this repo (loaded from
		// persistence), skip the first-run suppression.
		stats := dedup.Stats()
		if stats[repo.FullName()] == 0 {
			firstRun[repo.FullName()] = true
		}
	}

	return &GitHubWatcher{
		client:   client,
		config:   cfg,
		dedup:    dedup,
		firstRun: firstRun,
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

	// On first run for a repo with no persisted state, mark all current
	// events as seen without pitching them to avoid flooding.
	w.mu.Lock()
	isFirstRun := w.firstRun[repo.FullName()]
	if isFirstRun {
		w.firstRun[repo.FullName()] = false
	}
	w.mu.Unlock()

	if isFirstRun {
		for _, event := range events {
			w.dedup.Mark(repo.FullName(), event.GetID())
		}
		logger.Info("first run: marked existing events as seen", "count", len(events))
		return
	}

	var newCount int
	for _, event := range events {
		eventID := event.GetID()

		// Skip already-seen events.
		if w.dedup.Seen(repo.FullName(), eventID) {
			continue
		}

		kind := eventTypeToKind(event.GetType())
		if kind == "" || !repo.WatchesEvent(kind) {
			// Mark as seen even if we don't care about this event type,
			// so we don't re-evaluate it on every poll.
			w.dedup.Mark(repo.FullName(), eventID)
			continue
		}

		msg := eventToMessage(event, repo)
		select {
		case msgs <- msg:
			w.dedup.Mark(repo.FullName(), eventID)
			newCount++
		case <-ctx.Done():
			return
		}
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

// eventToMessage converts a GitHub event into a homerun Message with
// rich, event-type-specific fields parsed from the event payload.
func eventToMessage(event *github.Event, repo RepoConfig) homerun.Message {
	kind := eventTypeToKind(event.GetType())
	actor := ""
	if event.GetActor() != nil {
		actor = event.GetActor().GetLogin()
	}

	title, body, severity, url := parseEventPayload(event, repo)

	return homerun.Message{
		Title:     title,
		Message:   body,
		Severity:  severity,
		Author:    actor,
		Timestamp: event.GetCreatedAt().Format(time.RFC3339),
		System:    "homerun2-git-pitcher",
		Tags:      fmt.Sprintf("github,%s,%s", kind, repo.FullName()),
		Url:       url,
	}
}

// parseEventPayload extracts title, body, severity, and URL from the event
// payload based on event type. Falls back to generic info if parsing fails.
func parseEventPayload(event *github.Event, repo RepoConfig) (title, body, severity, url string) {
	repoURL := fmt.Sprintf("https://github.com/%s", repo.FullName())
	severity = "info"
	url = repoURL

	payload, err := event.ParsePayload()
	if err != nil {
		title = fmt.Sprintf("[%s] %s on %s", eventTypeToKind(event.GetType()), event.GetType(), repo.FullName())
		body = fmt.Sprintf("Event %s (payload parse error: %v)", event.GetID(), err)
		return
	}

	switch p := payload.(type) {
	case *github.PushEvent:
		branch := ""
		if ref := p.GetRef(); ref != "" {
			// Strip "refs/heads/" prefix.
			if len(ref) > 11 && ref[:11] == "refs/heads/" {
				branch = ref[11:]
			} else {
				branch = ref
			}
		}
		commits := len(p.Commits)
		title = fmt.Sprintf("Push to %s on %s", branch, repo.FullName())
		body = fmt.Sprintf("%d commit(s) pushed by %s", commits, p.GetPusher().GetName())
		if p.GetHeadCommit() != nil {
			body += fmt.Sprintf(": %s", p.GetHeadCommit().GetMessage())
		}
		url = p.GetCompare()
		if url == "" {
			url = repoURL

		}

	case *github.PullRequestEvent:
		pr := p.GetPullRequest()
		action := p.GetAction()
		title = fmt.Sprintf("PR #%d: %s (%s) on %s", pr.GetNumber(), pr.GetTitle(), action, repo.FullName())
		body = fmt.Sprintf("PR %s by %s: %s → %s",
			action,
			pr.GetUser().GetLogin(),
			pr.GetHead().GetRef(),
			pr.GetBase().GetRef(),
		)
		switch action {
		case "opened":
			severity = "info"
		case "closed":
			if pr.GetMerged() {
				severity = "success"
				body = fmt.Sprintf("PR merged by %s: %s → %s", pr.GetMergedBy().GetLogin(), pr.GetHead().GetRef(), pr.GetBase().GetRef())
			} else {
				severity = "warning"
			}
		}
		url = pr.GetHTMLURL()

	case *github.ReleaseEvent:
		rel := p.GetRelease()
		title = fmt.Sprintf("Release %s on %s", rel.GetTagName(), repo.FullName())
		body = rel.GetName()
		if relBody := rel.GetBody(); relBody != "" {
			if len(relBody) > 200 {
				relBody = relBody[:200] + "..."
			}
			body += "\n" + relBody
		}
		severity = "success"
		url = rel.GetHTMLURL()

	case *github.WorkflowRunEvent:
		run := p.GetWorkflowRun()
		conclusion := run.GetConclusion()
		title = fmt.Sprintf("Workflow %s %s on %s", run.GetName(), conclusion, repo.FullName())
		body = fmt.Sprintf("Workflow run #%d on branch %s: %s",
			run.GetRunNumber(),
			run.GetHeadBranch(),
			conclusion,
		)
		switch conclusion {
		case "success":
			severity = "success"
		case "failure":
			severity = "error"
		case "cancelled", "skipped":
			severity = "warning"
		}
		url = run.GetHTMLURL()

	default:
		title = fmt.Sprintf("[%s] %s on %s", eventTypeToKind(event.GetType()), event.GetType(), repo.FullName())
		body = fmt.Sprintf("Event %s", event.GetID())
	}

	return
}
