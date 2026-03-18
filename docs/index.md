# homerun2-git-pitcher

Active GitHub repository watcher for homerun2 — polls GitHub API and pitches events to Redis Streams.

## Architecture

```
                    ┌──────────────────────────────────┐
                    │     homerun2-git-pitcher          │
                    │                                    │
  GitHub API ──────>│  GitHubWatcher (per-repo pollers) │
  (poll events)     │         │                          │
                    │         v                          │
                    │  DedupStore (skip seen events)     │
                    │         │                          │
                    │         v                          │
                    │  Bridge ──> Pitcher (Redis/File)   │──> Redis Stream
                    │                                    │
  HTTP clients ────>│  /health  /pitch                   │
                    └──────────────────────────────────┘
```

## Quick start

```bash
# 1. Set up environment
export GITHUB_TOKEN=ghp_...
export AUTH_TOKEN=mysecret
export REDIS_ADDR=localhost REDIS_PORT=6379 REDIS_STREAM=messages

# 2. Point to a watch profile
export WATCH_CONFIG=./tests/watch-profile.yaml

# 3. Run
go run .
```

The watcher starts polling configured GitHub repos and pitches new events to Redis Streams. The HTTP server runs alongside for health checks and manual message submission.

## Dev mode (no Redis, no GitHub)

```bash
PITCHER_MODE=file AUTH_TOKEN=test go run .
```

Messages are written to `pitched.log` as JSON lines.

## Features

- **GitHub event polling** with configurable per-repo intervals
- **Event deduplication** with file-based persistence across restarts
- **Rate limit monitoring** with automatic backoff when approaching limits
- **Rich event mapping** — pushes, PRs, releases, workflow runs → structured messages
- **Dual mode** — watcher + HTTP API run side by side
- **Health endpoint** with version info and rate limit status
