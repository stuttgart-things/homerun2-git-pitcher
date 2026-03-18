# homerun2-git-pitcher

Active GitHub repository watcher for homerun2 — polls GitHub API and pitches events to Redis Streams.

[![Build & Test](https://github.com/stuttgart-things/homerun2-git-pitcher/actions/workflows/build-test.yaml/badge.svg)](https://github.com/stuttgart-things/homerun2-git-pitcher/actions/workflows/build-test.yaml)

## What it does

homerun2-git-pitcher watches configured GitHub repositories for events (pushes, pull requests, releases, workflow runs) and forwards them as structured messages to Redis Streams, where downstream homerun2 catchers can consume them.

```
GitHub API  ──poll──>  git-pitcher  ──pitch──>  Redis Stream  ──consume──>  catchers
```

## Usage

### Watch profile

Create a YAML file defining which repos to watch:

```yaml
github:
  token: env:GITHUB_TOKEN   # reads from environment variable

  repos:
    - owner: stuttgart-things
      name: homerun2-led-catcher
      interval: 5m
      events: [push, pull_request, release, workflow_run]

    - owner: stuttgart-things
      name: homerun2-core-catcher
      interval: 10m
      events: [push, release]

    - owner: hzeller
      name: rpi-rgb-led-matrix
      interval: 30m
      events: [release]
```

**Fields:**

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `github.token` | yes | — | GitHub PAT or `env:VAR_NAME` to read from env |
| `repos[].owner` | yes | — | GitHub organization or user |
| `repos[].name` | yes | — | Repository name |
| `repos[].interval` | no | `5m` | Poll interval (minimum `30s`) |
| `repos[].events` | no | all 4 types | Event types to watch |

**Supported event types:** `push`, `pull_request`, `release`, `workflow_run`

### Run with watcher

```bash
export GITHUB_TOKEN=ghp_...
export AUTH_TOKEN=mysecret
export WATCH_CONFIG=./tests/watch-profile.yaml
export DEDUP_STATE_FILE=./dedup-state.json

go run .
```

### Run without watcher (HTTP API only)

```bash
export REDIS_ADDR=localhost REDIS_PORT=6379 REDIS_STREAM=messages
export AUTH_TOKEN=mysecret
go run .
```

### Dev mode (no Redis)

```bash
PITCHER_MODE=file AUTH_TOKEN=test go run .
```

## API Endpoints

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/health` | `GET` | None | Health check with version, rate limit status |
| `/pitch` | `POST` | Bearer token | Submit a message to Redis Streams |

<details>
<summary><b>Pitch a message</b></summary>

```bash
curl -X POST http://localhost:8080/pitch \
  -H "Authorization: Bearer $AUTH_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Test Notification",
    "message": "Hello from homerun2-git-pitcher",
    "severity": "info",
    "author": "test"
  }'
```

</details>

<details>
<summary><b>Health check (with rate limit)</b></summary>

```bash
curl -s http://localhost:8080/health | jq .
```

```json
{
  "status": "healthy",
  "version": "1.2.0",
  "commit": "abc1234",
  "rateLimit": {
    "limit": 5000,
    "remaining": 4850,
    "reset": "2026-03-18T09:00:00Z",
    "backingOff": false
  }
}
```

</details>

## Event message mapping

| GitHub Event | Title | Severity | URL |
|-------------|-------|----------|-----|
| Push | `Push to {branch} on {repo}` | info | compare URL |
| Pull Request | `PR #{n}: {title} ({action})` | info / success (merged) / warning (closed) | PR URL |
| Release | `Release {tag} on {repo}` | success | release URL |
| Workflow Run | `Workflow {name} {conclusion}` | success / error / warning | run URL |

## Configuration reference

### Core

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | HTTP server port | `8080` |
| `AUTH_TOKEN` | Bearer token for `/pitch` endpoint | **required** |
| `PITCHER_MODE` | Backend: `redis` or `file` | `redis` |
| `PITCHER_FILE` | Output file path (file mode only) | `pitched.log` |

### Redis

| Variable | Description | Default |
|----------|-------------|---------|
| `REDIS_ADDR` | Redis server address | `localhost` |
| `REDIS_PORT` | Redis server port | `6379` |
| `REDIS_PASSWORD` | Redis password | (empty) |
| `REDIS_STREAM` | Redis stream name | `messages` |

### Watcher

| Variable | Description | Default |
|----------|-------------|---------|
| `WATCH_CONFIG` | Path to YAML watch profile | (disabled) |
| `DEDUP_STATE_FILE` | Path to persist dedup state | (in-memory only) |

### Logging

| Variable | Description | Default |
|----------|-------------|---------|
| `LOG_FORMAT` | `json` or `text` | `json` |
| `LOG_LEVEL` | `debug`, `info`, `warn`, `error` | `info` |
| `LOG_HEALTH_CHECKS` | Set `true` to log health requests | `false` |

## Development

```bash
# Unit tests (no Redis needed)
go test ./internal/...

# Lint
task lint

# Run locally with file backend
task run-local
```

## KCL Deployment

Kubernetes manifests are generated using [KCL](https://kcl-lang.io/). See [kcl/README.md](kcl/README.md) for full configuration reference.

```bash
# Render manifests
kcl run kcl/ -Y tests/kcl-deploy-profile.yaml

# Apply to cluster
kcl run kcl/ -Y tests/kcl-deploy-profile.yaml \
  -D 'config.namespace=homerun2' \
  -D 'config.redisPassword=<password>' \
  -D 'config.githubToken=<token>' \
  | python3 -c "
import yaml, sys
data = yaml.safe_load(sys.stdin)
for m in data['manifests']:
    print('---')
    print(yaml.dump(m, default_flow_style=False).rstrip())
" | kubectl apply -f -
```

For production, use the [Flux component](https://github.com/stuttgart-things/flux/tree/main/apps/homerun2/components/git-pitcher) which references the KCL OCI artifact and applies cluster-specific patches.

See [docs/](docs/) for full development, deployment, and CI/CD documentation.

## Links

- [Releases](https://github.com/stuttgart-things/homerun2-git-pitcher/releases)
- [homerun-library](https://github.com/stuttgart-things/homerun-library)
- [homerun2 Flux app](https://github.com/stuttgart-things/flux/tree/main/apps/homerun2)
