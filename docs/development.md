# Development

## Prerequisites

- Go 1.25+
- [Task](https://taskfile.dev/) (optional, for Taskfile commands)
- [Dagger](https://dagger.io/) (optional, for CI pipelines locally)
- Redis (only for `redis` pitcher mode)

## Project structure

```
├── main.go                    # Entry point: HTTP server + watcher startup
├── internal/
│   ├── banner/                # CLI startup banner animation
│   ├── config/                # Environment config loading (Redis, logging)
│   ├── handlers/              # HTTP handlers (/health, /pitch)
│   ├── middleware/            # Auth (Bearer token) and request logging
│   ├── models/                # Response types (PitchResponse)
│   ├── pitcher/               # Message delivery backends (Redis, File)
│   └── watcher/               # GitHub polling, dedup, rate limiting
│       ├── config.go          # YAML watch profile loader + validation
│       ├── watcher.go         # GitWatcher interface
│       ├── github.go          # GitHubWatcher implementation
│       ├── dedup.go           # Event deduplication store
│       ├── ratelimit.go       # Rate limit monitor + backoff
│       └── bridge.go          # Watcher → Pitcher bridge
├── dagger/                    # Dagger CI module (lint, build, test, scan)
├── tests/
│   └── watch-profile.yaml    # Example watch configuration
└── .ko.yaml                   # ko build configuration
```

## Running locally

### With file backend (no Redis)

```bash
task run-local
# or manually:
PITCHER_MODE=file AUTH_TOKEN=test LOG_FORMAT=text go run .
```

### With Redis

```bash
# Start Redis via Dagger
task run-redis-as-service

# In another terminal
export REDIS_ADDR=localhost REDIS_PORT=6379 REDIS_STREAM=messages
export AUTH_TOKEN=mysecret
go run .
```

### With GitHub watcher

```bash
export GITHUB_TOKEN=ghp_...
export AUTH_TOKEN=mysecret
export WATCH_CONFIG=./tests/watch-profile.yaml
export DEDUP_STATE_FILE=./dedup-state.json
go run .
```

## Testing

```bash
# Unit tests (no Redis needed)
go test ./internal/...

# Verbose with test names
go test -v ./internal/watcher/...

# Integration tests via Dagger (spins up Redis)
task build-test-binary

# Lint
task lint

# Build + scan container image
task build-scan-image-ko
```

## Watch profile configuration

The watch profile is a YAML file defining which GitHub repos to monitor:

```yaml
github:
  token: env:GITHUB_TOKEN   # reads GITHUB_TOKEN from environment

  repos:
    - owner: stuttgart-things
      name: homerun2-led-catcher
      interval: 5m
      events:
        - push
        - pull_request
        - release
        - workflow_run

    - owner: hzeller
      name: rpi-rgb-led-matrix
      interval: 30m           # longer interval for external repos
      events:
        - release
```

### Token resolution

The `token` field supports `env:VAR_NAME` syntax to read the value from an environment variable at startup. This avoids putting tokens in config files.

### Defaults

| Field | Default when omitted |
|-------|---------------------|
| `interval` | `5m` |
| `events` | all 4 types (`push`, `pull_request`, `release`, `workflow_run`) |

### Validation rules

- `token` is required and must resolve to a non-empty value
- At least one repo must be configured
- Each repo must have `owner` and `name`
- `interval` must be >= `30s`
- Event types must be one of: `push`, `pull_request`, `release`, `workflow_run`

## Event deduplication

The dedup store prevents the same GitHub event from being pitched twice. It tracks event IDs per repository with configurable limits:

- **Max events per repo:** 1000 (oldest evicted when exceeded)
- **Retention:** 24 hours (expired entries removed)
- **Persistence:** set `DEDUP_STATE_FILE` to a file path; state is saved as JSON on shutdown and reloaded on startup
- **First-run suppression:** on the first poll for a repo with no persisted state, all existing events are marked as seen without pitching

## Rate limit monitoring

The watcher tracks GitHub API rate limits and automatically backs off when remaining requests drop below 100:

- All polling goroutines pause until the rate limit resets
- Backoff is logged with structured fields (`remaining`, `limit`, `resetAt`)
- The `/health` endpoint exposes current rate limit status
- Rate limit info is updated after every API response

## Environment variables

### Core

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | HTTP server port | `8080` |
| `AUTH_TOKEN` | Bearer token for `/pitch` | **required** |
| `PITCHER_MODE` | `redis` or `file` | `redis` |
| `PITCHER_FILE` | Output path for file mode | `pitched.log` |

### Redis

| Variable | Description | Default |
|----------|-------------|---------|
| `REDIS_ADDR` | Redis address | `localhost` |
| `REDIS_PORT` | Redis port | `6379` |
| `REDIS_PASSWORD` | Redis password | (empty) |
| `REDIS_STREAM` | Stream name | `messages` |

### Watcher

| Variable | Description | Default |
|----------|-------------|---------|
| `WATCH_CONFIG` | Path to YAML watch profile | (disabled) |
| `DEDUP_STATE_FILE` | Dedup persistence path | (in-memory only) |

### Logging

| Variable | Description | Default |
|----------|-------------|---------|
| `LOG_FORMAT` | `json` or `text` | `json` |
| `LOG_LEVEL` | `debug`, `info`, `warn`, `error` | `info` |
| `LOG_HEALTH_CHECKS` | Log health check requests | `false` |

## Code conventions

- No Dockerfile — use [ko](https://ko.build/) for container images
- Config via environment variables, loaded once at startup
- Interfaces for testability (`Pitcher`, `GitWatcher`, `DedupStore`)
- Unit tests must not require Redis
- Conventional commits: `fix:`, `feat:`, `test:`, `chore:`, `docs:`
