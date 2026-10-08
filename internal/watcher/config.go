package watcher

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// WatchConfig is the top-level configuration for the git-pitcher watcher.
type WatchConfig struct {
	// Stream is the default Redis stream name for all published events.
	// If empty, the pitcher falls back to REDIS_STREAM / its hardcoded default.
	Stream string       `yaml:"stream"`
	GitHub GitHubConfig `yaml:"github"`
}

// GitHubConfig holds GitHub-specific configuration.
type GitHubConfig struct {
	// Token is the GitHub PAT. Supports "env:VAR_NAME" syntax to read from env.
	Token string       `yaml:"token"`
	Orgs  []OrgConfig  `yaml:"orgs"`
	Repos []RepoConfig `yaml:"repos"`
}

// OrgConfig watches every public repo of an organization through one call
// per poll (GET /orgs/{org}/events). Private repos are not in that feed;
// list them under repos. A repo listed under repos is skipped here, so its
// own settings win and nothing is pitched twice.
type OrgConfig struct {
	Name     string        `yaml:"name"`
	Interval time.Duration `yaml:"interval"`
	Events   []EventKind   `yaml:"events"`
	// Stream overrides the top-level WatchConfig.Stream for this org.
	Stream string `yaml:"stream"`
	// ExcludeActors drops events by these logins, e.g. "renovate[bot]".
	ExcludeActors []string `yaml:"exclude_actors"`
	// ExcludeRepos drops events of these repos, by name or owner/name.
	ExcludeRepos []string `yaml:"exclude_repos"`
}

// DedupKey is the key under which the dedup store keeps this org's events.
func (o OrgConfig) DedupKey() string {
	return "org:" + o.Name
}

// RepoConfig defines a single repository to watch.
type RepoConfig struct {
	Owner    string        `yaml:"owner"`
	Name     string        `yaml:"name"`
	Interval time.Duration `yaml:"interval"`
	Events   []EventKind   `yaml:"events"`
	// Stream overrides the top-level WatchConfig.Stream for this repo.
	Stream string `yaml:"stream"`
	// Labels restricts issue_comment events to issues carrying at least one
	// of these labels. Empty means every issue. Other event kinds ignore it.
	Labels []string `yaml:"labels"`
}

// ResolveStream returns the stream for events from this repo.
// Precedence: repo-level override → top-level default → "" (caller falls back).
func (c *WatchConfig) ResolveStream(repo RepoConfig) string {
	if repo.Stream != "" {
		return repo.Stream
	}
	return c.Stream
}

// EventKind represents a type of GitHub event to watch.
type EventKind string

const (
	EventPush        EventKind = "push"
	EventPullRequest EventKind = "pull_request"
	EventRelease     EventKind = "release"
	EventWorkflowRun EventKind = "workflow_run"
	// EventIssueComment is opt-in: it is not part of the default events.
	EventIssueComment EventKind = "issue_comment"
)

// FullName returns "owner/name".
func (r RepoConfig) FullName() string {
	return r.Owner + "/" + r.Name
}

// WatchesEvent returns true if this repo watches the given event kind.
func (r RepoConfig) WatchesEvent(kind EventKind) bool {
	for _, e := range r.Events {
		if e == kind {
			return true
		}
	}
	return false
}

// MatchesLabels returns true if no label filter is set or if one of the
// given labels is in it.
func (r RepoConfig) MatchesLabels(labels []string) bool {
	if len(r.Labels) == 0 {
		return true
	}
	for _, want := range r.Labels {
		for _, got := range labels {
			if want == got {
				return true
			}
		}
	}
	return false
}

// DedupKeys returns the dedup store keys of every configured repo and org.
func (c *WatchConfig) DedupKeys() []string {
	keys := make([]string, 0, len(c.GitHub.Repos)+len(c.GitHub.Orgs))
	for _, repo := range c.GitHub.Repos {
		keys = append(keys, repo.FullName())
	}
	for _, org := range c.GitHub.Orgs {
		keys = append(keys, org.DedupKey())
	}
	return keys
}

// LoadWatchConfig loads the watch configuration from a YAML file.
func LoadWatchConfig(path string) (*WatchConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	var cfg WatchConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", path, err)
	}

	// Resolve token from env if prefixed with "env:"
	if len(cfg.GitHub.Token) > 4 && cfg.GitHub.Token[:4] == "env:" {
		envVar := cfg.GitHub.Token[4:]
		cfg.GitHub.Token = os.Getenv(envVar)
		if cfg.GitHub.Token == "" {
			return nil, fmt.Errorf("environment variable %s is not set (referenced in config token)", envVar)
		}
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// Validate checks the configuration for required fields and valid values.
func (c *WatchConfig) Validate() error {
	if c.GitHub.Token == "" {
		return fmt.Errorf("github.token is required")
	}

	if len(c.GitHub.Repos) == 0 && len(c.GitHub.Orgs) == 0 {
		return fmt.Errorf("github.repos or github.orgs must contain at least one entry")
	}

	for i, org := range c.GitHub.Orgs {
		if org.Name == "" {
			return fmt.Errorf("github.orgs[%d].name is required", i)
		}
		interval, err := validInterval(org.Interval)
		if err != nil {
			return fmt.Errorf("github.orgs[%d].%w", i, err)
		}
		c.GitHub.Orgs[i].Interval = interval
		events, err := validEvents(org.Events)
		if err != nil {
			return fmt.Errorf("github.orgs[%d].%w", i, err)
		}
		c.GitHub.Orgs[i].Events = events
	}

	for i, repo := range c.GitHub.Repos {
		if repo.Owner == "" {
			return fmt.Errorf("github.repos[%d].owner is required", i)
		}
		if repo.Name == "" {
			return fmt.Errorf("github.repos[%d].name is required", i)
		}
		interval, err := validInterval(repo.Interval)
		if err != nil {
			return fmt.Errorf("github.repos[%d].%w", i, err)
		}
		c.GitHub.Repos[i].Interval = interval
		events, err := validEvents(repo.Events)
		if err != nil {
			return fmt.Errorf("github.repos[%d].%w", i, err)
		}
		c.GitHub.Repos[i].Events = events
	}

	return nil
}

// validInterval defaults an unset poll interval to 5 minutes and rejects
// one below 30s.
func validInterval(d time.Duration) (time.Duration, error) {
	if d <= 0 {
		return 5 * time.Minute, nil
	}
	if d < 30*time.Second {
		return 0, fmt.Errorf("interval must be >= 30s (got %s)", d)
	}
	return d, nil
}

// validEvents defaults unset events to all but the opt-in issue_comment and
// rejects unknown kinds.
func validEvents(events []EventKind) ([]EventKind, error) {
	if len(events) == 0 {
		return []EventKind{EventPush, EventPullRequest, EventRelease, EventWorkflowRun}, nil
	}
	for _, ev := range events {
		switch ev {
		case EventPush, EventPullRequest, EventRelease, EventWorkflowRun, EventIssueComment:
			// valid
		default:
			return nil, fmt.Errorf("events: unknown event kind %q (valid: push, pull_request, release, workflow_run, issue_comment)", ev)
		}
	}
	return events, nil
}
