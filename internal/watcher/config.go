package watcher

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// WatchConfig is the top-level configuration for the git-pitcher watcher.
type WatchConfig struct {
	GitHub GitHubConfig `yaml:"github"`
}

// GitHubConfig holds GitHub-specific configuration.
type GitHubConfig struct {
	// Token is the GitHub PAT. Supports "env:VAR_NAME" syntax to read from env.
	Token string       `yaml:"token"`
	Repos []RepoConfig `yaml:"repos"`
}

// RepoConfig defines a single repository to watch.
type RepoConfig struct {
	Owner    string        `yaml:"owner"`
	Name     string        `yaml:"name"`
	Interval time.Duration `yaml:"interval"`
	Events   []EventKind   `yaml:"events"`
}

// EventKind represents a type of GitHub event to watch.
type EventKind string

const (
	EventPush        EventKind = "push"
	EventPullRequest EventKind = "pull_request"
	EventRelease     EventKind = "release"
	EventWorkflowRun EventKind = "workflow_run"
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

	if len(c.GitHub.Repos) == 0 {
		return fmt.Errorf("github.repos must contain at least one repository")
	}

	for i, repo := range c.GitHub.Repos {
		if repo.Owner == "" {
			return fmt.Errorf("github.repos[%d].owner is required", i)
		}
		if repo.Name == "" {
			return fmt.Errorf("github.repos[%d].name is required", i)
		}
		if repo.Interval <= 0 {
			// Default to 5 minutes
			c.GitHub.Repos[i].Interval = 5 * time.Minute
		} else if repo.Interval < 30*time.Second {
			return fmt.Errorf("github.repos[%d].interval must be >= 30s (got %s)", i, repo.Interval)
		}
		if len(repo.Events) == 0 {
			// Default to all events
			c.GitHub.Repos[i].Events = []EventKind{EventPush, EventPullRequest, EventRelease, EventWorkflowRun}
		}
		for _, ev := range repo.Events {
			switch ev {
			case EventPush, EventPullRequest, EventRelease, EventWorkflowRun:
				// valid
			default:
				return fmt.Errorf("github.repos[%d].events: unknown event kind %q (valid: push, pull_request, release, workflow_run)", i, ev)
			}
		}
	}

	return nil
}
