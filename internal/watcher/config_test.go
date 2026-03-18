package watcher

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadWatchConfig(t *testing.T) {
	yaml := `
github:
  token: test-token-123
  repos:
    - owner: stuttgart-things
      name: homerun2-led-catcher
      interval: 5m
      events: [push, pull_request, release]
    - owner: stuttgart-things
      name: homerun2-core-catcher
      interval: 10m
      events: [push, release, workflow_run]
`
	path := filepath.Join(t.TempDir(), "watch.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	cfg, err := LoadWatchConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.GitHub.Token != "test-token-123" {
		t.Errorf("expected token 'test-token-123', got %q", cfg.GitHub.Token)
	}
	if len(cfg.GitHub.Repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(cfg.GitHub.Repos))
	}
	if cfg.GitHub.Repos[0].FullName() != "stuttgart-things/homerun2-led-catcher" {
		t.Errorf("unexpected repo name: %s", cfg.GitHub.Repos[0].FullName())
	}
	if cfg.GitHub.Repos[0].Interval != 5*time.Minute {
		t.Errorf("expected 5m interval, got %s", cfg.GitHub.Repos[0].Interval)
	}
	if len(cfg.GitHub.Repos[0].Events) != 3 {
		t.Errorf("expected 3 events, got %d", len(cfg.GitHub.Repos[0].Events))
	}
	if !cfg.GitHub.Repos[0].WatchesEvent(EventPush) {
		t.Error("expected repo to watch push events")
	}
	if cfg.GitHub.Repos[0].WatchesEvent(EventWorkflowRun) {
		t.Error("repo should not watch workflow_run events")
	}
}

func TestLoadWatchConfigEnvToken(t *testing.T) {
	t.Setenv("TEST_GH_TOKEN", "from-env-token")

	yaml := `
github:
  token: env:TEST_GH_TOKEN
  repos:
    - owner: test
      name: repo
      interval: 1m
      events: [push]
`
	path := filepath.Join(t.TempDir(), "watch.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	cfg, err := LoadWatchConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.GitHub.Token != "from-env-token" {
		t.Errorf("expected 'from-env-token', got %q", cfg.GitHub.Token)
	}
}

func TestLoadWatchConfigDefaults(t *testing.T) {
	yaml := `
github:
  token: tok
  repos:
    - owner: org
      name: repo
`
	path := filepath.Join(t.TempDir(), "watch.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	cfg, err := LoadWatchConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Default interval
	if cfg.GitHub.Repos[0].Interval != 5*time.Minute {
		t.Errorf("expected default 5m interval, got %s", cfg.GitHub.Repos[0].Interval)
	}
	// Default events (all)
	if len(cfg.GitHub.Repos[0].Events) != 4 {
		t.Errorf("expected 4 default events, got %d", len(cfg.GitHub.Repos[0].Events))
	}
}

func TestLoadWatchConfigValidation(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{"no token", `github: {token: "", repos: [{owner: o, name: n, interval: 1m, events: [push]}]}`},
		{"no repos", `github: {token: tok, repos: []}`},
		{"no owner", `github: {token: tok, repos: [{name: n, interval: 1m, events: [push]}]}`},
		{"no name", `github: {token: tok, repos: [{owner: o, interval: 1m, events: [push]}]}`},
		{"bad interval", `github: {token: tok, repos: [{owner: o, name: n, interval: 5s, events: [push]}]}`},
		{"bad event", `github: {token: tok, repos: [{owner: o, name: n, interval: 1m, events: [push, invalid]}]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "watch.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0644); err != nil {
				t.Fatalf("failed to write test file: %v", err)
			}
			_, err := LoadWatchConfig(path)
			if err == nil {
				t.Error("expected validation error, got nil")
			}
		})
	}
}
