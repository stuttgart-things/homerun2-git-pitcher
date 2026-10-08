package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/go-github/v87/github"
	homerun "github.com/stuttgart-things/homerun-library/v4"
)

// GitHubWatcher implements GitWatcher by polling the GitHub API.
type GitHubWatcher struct {
	client    *github.Client
	config    *WatchConfig
	dedup     DedupStore
	RateLimit *RateLimitMonitor

	// firstRun tracks whether a repo has completed its initial poll.
	// On the first poll we mark events as seen without pitching them.
	mu       sync.Mutex
	firstRun map[string]bool

	now func() time.Time
}

// NewGitHubWatcher creates a watcher from the given config.
// If dedup is nil, a default in-memory store is used.
func NewGitHubWatcher(cfg *WatchConfig, dedup DedupStore) (*GitHubWatcher, error) {
	client, err := github.NewClient(github.WithAuthToken(cfg.GitHub.Token))
	if err != nil {
		return nil, fmt.Errorf("create github client: %w", err)
	}

	if dedup == nil {
		dedup, _ = NewMemoryDedupStore(DefaultDedupConfig(), "")
	}

	keys := cfg.DedupKeys()
	firstRun := make(map[string]bool, len(keys))
	stats := dedup.Stats()
	for _, key := range keys {
		// If the dedup store already has state for this repo or org (loaded
		// from persistence), skip the first-run suppression.
		if stats[key] == 0 {
			firstRun[key] = true
		}
	}

	return &GitHubWatcher{
		client:    client,
		config:    cfg,
		dedup:     dedup,
		RateLimit: NewRateLimitMonitor(DefaultBackoffThreshold),
		firstRun:  firstRun,
		now:       time.Now,
	}, nil
}

// Watch starts a goroutine per configured repo and org that polls for events at the
// configured interval. Events are converted to homerun Messages and sent on
// the returned channel. Polling stops when ctx is cancelled.
func (w *GitHubWatcher) Watch(ctx context.Context) (<-chan PitchEvent, error) {
	msgs := make(chan PitchEvent, 100)

	var wg sync.WaitGroup
	for _, repo := range w.config.GitHub.Repos {
		wg.Add(1)
		go func(repo RepoConfig) {
			defer wg.Done()
			w.pollRepo(ctx, repo, msgs)
		}(repo)
	}
	for _, org := range w.config.GitHub.Orgs {
		wg.Add(1)
		go func(org OrgConfig) {
			defer wg.Done()
			w.pollOrg(ctx, org, msgs)
		}(org)
	}

	go func() {
		wg.Wait()
		close(msgs)
	}()

	return msgs, nil
}

// pollRepo polls a single repo at its configured interval.
func (w *GitHubWatcher) pollRepo(ctx context.Context, repo RepoConfig, msgs chan<- PitchEvent) {
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
			// Wait if rate limit is low before making another API call.
			if err := w.RateLimit.WaitIfNeeded(ctx); err != nil {
				return
			}
			w.fetchAndSend(ctx, repo, msgs)
		}
	}
}

// fetchAndSend fetches recent events from GitHub and sends new ones as messages.
func (w *GitHubWatcher) fetchAndSend(ctx context.Context, repo RepoConfig, msgs chan<- PitchEvent) {
	logger := slog.With("repo", repo.FullName())

	events, resp, err := w.client.Activity.ListRepositoryEvents(ctx, repo.Owner, repo.Name, &github.ListOptions{
		PerPage: 30,
	})
	if err != nil {
		logger.Error("failed to list events", "error", err)
		return
	}

	// Update rate limit monitor from response.
	w.updateRateLimit(resp)

	// Handle conditional requests (304 Not Modified).
	if resp.StatusCode == 304 {
		logger.Debug("no new events (304)")
		return
	}

	w.processEvents(ctx, repo, events, msgs)
}

// processEvents pitches the events of one poll of a repo that have not been
// seen yet.
func (w *GitHubWatcher) processEvents(ctx context.Context, repo RepoConfig, events []*github.Event, msgs chan<- PitchEvent) {
	w.processFeed(ctx, repo.FullName(), events, msgs, func(*github.Event) (RepoConfig, bool) {
		return repo, true
	})
}

// processFeed pitches the events of one poll of a feed (a repo or an org)
// that have not been seen yet. key is the feed's dedup key; repoFor returns
// the settings an event is judged by, or false to drop it.
func (w *GitHubWatcher) processFeed(ctx context.Context, key string, events []*github.Event, msgs chan<- PitchEvent, repoFor func(*github.Event) (RepoConfig, bool)) {
	logger := slog.With("feed", key)

	// On first run for a feed with no persisted state, mark all current
	// events as seen without pitching them to avoid flooding.
	w.mu.Lock()
	isFirstRun := w.firstRun[key]
	if isFirstRun {
		w.firstRun[key] = false
	}
	w.mu.Unlock()

	if isFirstRun {
		for _, event := range events {
			w.dedup.Mark(key, event.GetID(), w.createdAt(event))
		}
		logger.Info("first run: marked existing events as seen", "count", len(events))
		return
	}

	cutoff := w.now().Add(-w.dedup.Retention())
	var newCount, expiredCount int
	for _, event := range events {
		eventID := event.GetID()

		// Skip already-seen events.
		if w.dedup.Seen(key, eventID) {
			continue
		}

		// GitHub keeps listing the last 30 events for days. Once an event is
		// older than the retention window its entry may be gone, so "not
		// seen" no longer means new: never pitch it (#34).
		createdAt := w.createdAt(event)
		if createdAt.Before(cutoff) {
			expiredCount++
			continue
		}

		kind := eventTypeToKind(event.GetType())
		repo, ok := repoFor(event)
		if !ok || kind == "" || !repo.WatchesEvent(kind) || !wantsPayload(event, repo) {
			// Mark as seen even if we don't care about this event,
			// so we don't re-evaluate it on every poll.
			w.dedup.Mark(key, eventID, createdAt)
			continue
		}

		w.fillPayload(ctx, event, repo)
		msg := eventToMessage(event, repo)
		select {
		case msgs <- PitchEvent{Message: msg, Stream: w.config.ResolveStream(repo)}:
			w.dedup.Mark(key, eventID, createdAt)
			newCount++
		case <-ctx.Done():
			return
		}
	}

	if newCount > 0 {
		logger.Info("new events detected", "count", newCount)
	}
	if expiredCount > 0 {
		logger.Debug("skipped unseen events older than the dedup retention", "count", expiredCount, "retention", w.dedup.Retention())
	}
}

// createdAt returns the event's creation time, or now if GitHub sent none.
func (w *GitHubWatcher) createdAt(event *github.Event) time.Time {
	if t := event.GetCreatedAt().Time; !t.IsZero() {
		return t
	}
	return w.now()
}

// pollOrg polls an organization's public event feed at its interval.
func (w *GitHubWatcher) pollOrg(ctx context.Context, org OrgConfig, msgs chan<- PitchEvent) {
	logger := slog.With("org", org.Name)
	logger.Info("starting org watcher", "interval", org.Interval, "events", org.Events)

	w.fetchOrgAndSend(ctx, org, msgs)

	ticker := time.NewTicker(org.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("org watcher stopped")
			return
		case <-ticker.C:
			if err := w.RateLimit.WaitIfNeeded(ctx); err != nil {
				return
			}
			w.fetchOrgAndSend(ctx, org, msgs)
		}
	}
}

// orgPerPage is one page of the org feed. About 100 events in 34 hours
// were measured on stuttgart-things (2026-10-08), so one page per 5-minute
// poll leaves a wide margin.
const orgPerPage = 100

// fetchOrgAndSend fetches the org's recent public events and sends new ones.
func (w *GitHubWatcher) fetchOrgAndSend(ctx context.Context, org OrgConfig, msgs chan<- PitchEvent) {
	events, resp, err := w.client.Activity.ListEventsForOrganization(ctx, org.Name, &github.ListOptions{
		PerPage: orgPerPage,
	})
	w.updateRateLimit(resp)
	if err != nil {
		slog.Error("failed to list org events", "org", org.Name, "error", err)
		return
	}
	if resp.StatusCode == 304 {
		return
	}

	w.processFeed(ctx, org.DedupKey(), events, msgs, func(event *github.Event) (RepoConfig, bool) {
		return w.orgRepoFor(org, event)
	})
}

// orgRepoFor returns the settings an org event is judged by: the org's,
// applied to the event's repo. It drops events of excluded actors and
// repos, and of repos listed under repos, which their own watcher covers.
func (w *GitHubWatcher) orgRepoFor(org OrgConfig, event *github.Event) (RepoConfig, bool) {
	owner, name, ok := strings.Cut(event.GetRepo().GetName(), "/")
	if !ok {
		return RepoConfig{}, false
	}
	for _, actor := range org.ExcludeActors {
		if strings.EqualFold(actor, event.GetActor().GetLogin()) {
			return RepoConfig{}, false
		}
	}
	for _, ex := range org.ExcludeRepos {
		if strings.EqualFold(ex, name) || strings.EqualFold(ex, owner+"/"+name) {
			return RepoConfig{}, false
		}
	}
	for _, listed := range w.config.GitHub.Repos {
		if strings.EqualFold(listed.FullName(), owner+"/"+name) {
			return RepoConfig{}, false
		}
	}
	return RepoConfig{
		Owner:    owner,
		Name:     name,
		Interval: org.Interval,
		Events:   org.Events,
		Stream:   org.Stream,
	}, true
}

// fillPayload restores what GitHub's Events API stopped sending on
// 2025-10-07 (#64): a PullRequestEvent carries only number, url, base and
// head of its pull request, a PushEvent only before and head. It fetches the
// pull request or the compare of the push and writes it back into the
// event's raw payload, so eventToMessage keeps one code path for full and
// slim payloads. On an API error the event is pitched with what it has.
func (w *GitHubWatcher) fillPayload(ctx context.Context, event *github.Event, repo RepoConfig) {
	logger := slog.With("repo", repo.FullName(), "event", event.GetID())

	payload, err := event.ParsePayload()
	if err != nil {
		return
	}

	var filled any
	switch p := payload.(type) {
	case *github.PullRequestEvent:
		number := p.GetPullRequest().GetNumber()
		if number == 0 {
			number = p.GetNumber()
		}
		if p.GetPullRequest().GetTitle() != "" || number == 0 {
			return
		}
		pr, resp, err := w.client.PullRequests.Get(ctx, repo.Owner, repo.Name, number)
		w.updateRateLimit(resp)
		if err != nil {
			logger.Warn("failed to fetch pull request for slim event payload", "number", number, "error", err)
			return
		}
		p.PullRequest = pr
		filled = p

	case *github.PushEvent:
		if p.GetHeadCommit() != nil || p.GetBefore() == "" || p.GetHead() == "" {
			return
		}
		cmp, resp, err := w.client.Repositories.CompareCommits(ctx, repo.Owner, repo.Name, p.GetBefore(), p.GetHead(), nil)
		w.updateRateLimit(resp)
		if err != nil {
			// A new branch has before 0000…, which has nothing to compare.
			logger.Debug("failed to compare push for slim event payload", "error", err)
			return
		}
		p.Commits = nil
		for _, c := range cmp.Commits {
			p.Commits = append(p.Commits, &github.HeadCommit{
				ID:      c.SHA,
				Message: c.GetCommit().Message,
			})
		}
		if n := len(p.Commits); n > 0 {
			p.HeadCommit = p.Commits[n-1]
		}
		p.Compare = cmp.HTMLURL
		p.Pusher = &github.CommitAuthor{Name: github.Ptr(event.GetActor().GetLogin())}
		filled = p

	default:
		return
	}

	raw, err := json.Marshal(filled)
	if err != nil {
		return
	}
	rawMsg := json.RawMessage(raw)
	event.RawPayload = &rawMsg
}

// updateRateLimit feeds the rate limit of an API response to the monitor.
func (w *GitHubWatcher) updateRateLimit(resp *github.Response) {
	if resp != nil && resp.Rate.Limit > 0 {
		w.RateLimit.Update(resp.Rate)
	}
}

// wantsPayload applies the filters that need the event payload: an
// issue_comment is pitched only when it was created (not edited or deleted)
// and its issue carries one of the repo's labels.
func wantsPayload(event *github.Event, repo RepoConfig) bool {
	if event.GetType() != "IssueCommentEvent" {
		return true
	}
	payload, err := event.ParsePayload()
	if err != nil {
		// Let eventToMessage report the parse error.
		return true
	}
	p, ok := payload.(*github.IssueCommentEvent)
	if !ok || p.GetAction() != "created" {
		return false
	}
	var labels []string
	for _, l := range p.GetIssue().Labels {
		labels = append(labels, l.GetName())
	}
	return repo.MatchesLabels(labels)
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
	case "IssueCommentEvent":
		return EventIssueComment
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

	tags := fmt.Sprintf("github,%s,%s", kind, repo.FullName())
	if kind == EventIssueComment {
		body, tags = commentTags(body, tags)
	}

	return homerun.Message{
		Title:     title,
		Message:   body,
		Severity:  severity,
		Author:    actor,
		Timestamp: event.GetCreatedAt().Format(time.RFC3339),
		System:    "homerun2-git-pitcher",
		Tags:      tags,
		URL:       url,
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
		if pr.GetTitle() != "" {
			title = fmt.Sprintf("PR #%d: %s (%s) on %s", pr.GetNumber(), pr.GetTitle(), action, repo.FullName())
		} else {
			// Slim payload whose pull request could not be fetched.
			title = fmt.Sprintf("PR #%d (%s) on %s", pr.GetNumber(), action, repo.FullName())
		}
		author := pr.GetUser().GetLogin()
		if author == "" {
			author = event.GetActor().GetLogin()
		}
		body = fmt.Sprintf("PR %s by %s: %s → %s",
			action,
			author,
			pr.GetHead().GetRef(),
			pr.GetBase().GetRef(),
		)
		switch action {
		case "opened":
			severity = "info"
		case "merged":
			// Events API since 2025-10-07: a merge is its own action, not
			// closed with merged set.
			severity = "success"
			body = fmt.Sprintf("PR merged by %s: %s → %s", mergedBy(pr, event), pr.GetHead().GetRef(), pr.GetBase().GetRef())
		case "closed":
			if pr.GetMerged() {
				severity = "success"
				body = fmt.Sprintf("PR merged by %s: %s → %s", pr.GetMergedBy().GetLogin(), pr.GetHead().GetRef(), pr.GetBase().GetRef())
			} else {
				severity = "warning"
			}
		}
		url = pr.GetHTMLURL()
		if url == "" {
			url = fmt.Sprintf("%s/pull/%d", repoURL, pr.GetNumber())
		}

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

	case *github.IssueCommentEvent:
		issue := p.GetIssue()
		comment := p.GetComment()
		title = fmt.Sprintf("Comment on #%d: %s on %s", issue.GetNumber(), issue.GetTitle(), repo.FullName())
		body, severity = commentSeverity(comment.GetBody())
		url = comment.GetHTMLURL()

	default:
		title = fmt.Sprintf("[%s] %s on %s", eventTypeToKind(event.GetType()), event.GetType(), repo.FullName())
		body = fmt.Sprintf("Event %s", event.GetID())
	}

	return
}

// maxCommentLen caps the comment text in a message, in characters. Longer
// than a release body: a comment pitched on purpose (a report) is the
// message itself.
const maxCommentLen = 1000

// A comment can steer its own message with HTML comments, which GitHub does
// not render:
//
//	<!-- homerun2:severity=warning -->  severity instead of "info"
//	<!-- homerun2:tags=morning -->      extra tags, comma-separated
var (
	severityMarker = regexp.MustCompile(`<!--\s*homerun2:severity=(info|success|warning|error)\s*-->\s*`)
	tagsMarker     = regexp.MustCompile(`<!--\s*homerun2:tags=([A-Za-z0-9_.,-]+)\s*-->\s*`)
)

// commentSeverity returns the comment text without the severity marker and
// the severity it sets, or "info" if it has none.
func commentSeverity(text string) (string, string) {
	severity := "info"
	if m := severityMarker.FindStringSubmatch(text); m != nil {
		severity = m[1]
		text = severityMarker.ReplaceAllString(text, "")
	}
	return strings.TrimSpace(text), severity
}

// commentTags strips the tags marker from body, appends its tags to tags and
// truncates body to maxCommentLen. It runs after commentSeverity, on the
// already parsed body, so truncation never cuts a marker in half.
func commentTags(body, tags string) (string, string) {
	if m := tagsMarker.FindStringSubmatch(body); m != nil {
		for _, t := range strings.Split(m[1], ",") {
			if t != "" {
				tags += "," + t
			}
		}
		body = strings.TrimSpace(tagsMarker.ReplaceAllString(body, ""))
	}
	if utf8.RuneCountInString(body) > maxCommentLen {
		body = string([]rune(body)[:maxCommentLen]) + "..."
	}
	return body, tags
}

// mergedBy names who merged pr: the pull request's merged_by if the payload
// has it, else the event's actor, which for a merged event is the merger.
func mergedBy(pr *github.PullRequest, event *github.Event) string {
	if login := pr.GetMergedBy().GetLogin(); login != "" {
		return login
	}
	return event.GetActor().GetLogin()
}
