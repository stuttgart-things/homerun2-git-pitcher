# API Usage

## Endpoints

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/health` | `GET` | None | Health check with version and rate limit info |
| `/pitch` | `POST` | Bearer token | Submit a message to Redis Streams |

## Health check

```bash
curl -s http://localhost:8080/health | jq .
```

**Response:**

```json
{
  "status": "healthy",
  "time": "2026-03-18T08:30:00Z",
  "version": "1.2.0",
  "commit": "abc1234",
  "date": "2026-03-18",
  "rateLimit": {
    "limit": 5000,
    "remaining": 4850,
    "reset": "2026-03-18T09:00:00Z",
    "backingOff": false
  }
}
```

The `rateLimit` field is only present when the GitHub watcher is configured (`WATCH_CONFIG` is set). When `backingOff` is `true`, polling is paused until the rate limit resets.

## Pitch a message

Manually submit a message to Redis Streams:

```bash
curl -X POST http://localhost:8080/pitch \
  -H "Authorization: Bearer $AUTH_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Deploy completed",
    "message": "v1.2.0 deployed to production",
    "severity": "success",
    "author": "deploy-bot",
    "system": "ci",
    "tags": "deploy,production",
    "url": "https://github.com/org/repo/actions/runs/123"
  }'
```

**Response:**

```json
{
  "objectId": "1710748200000-0",
  "streamId": "messages",
  "status": "success"
}
```

### Message fields

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `title` | yes | — | Short event title |
| `message` | yes | — | Event description |
| `severity` | no | `info` | `info`, `success`, `warning`, `error` |
| `author` | no | `unknown` | Who triggered the event |
| `system` | no | `homerun2-git-pitcher` | Source system |
| `tags` | no | — | Comma-separated tags |
| `url` | no | — | Link to the event source |
| `assigneeaddress` | no | — | Notification target address |
| `assigneename` | no | — | Notification target name |
| `artifacts` | no | — | Related artifacts |

### Error responses

| Status | Reason |
|--------|--------|
| `400` | Missing required fields (`title`, `message`) |
| `401` | Missing or invalid `Authorization` header |
| `405` | Wrong HTTP method (only `POST` accepted) |
| `500` | Redis connection failure |

## GitHub watcher events

When the watcher is configured, it automatically pitches events from GitHub to Redis Streams. Each event type produces a message with specific fields:

| GitHub Event | Title format | Severity | URL points to |
|-------------|-------------|----------|---------------|
| Push | `Push to {branch} on {owner/repo}` | info | Compare URL |
| Pull Request opened | `PR #{n}: {title} (opened)` | info | PR URL |
| Pull Request merged | `PR #{n}: {title} (merged)`, message `PR merged by {login}: {head} → {base}` | success | PR URL |
| Pull Request closed | `PR #{n}: {title} (closed)` | warning | PR URL |
| Release | `Release {tag} on {owner/repo}` | success | Release URL |
| Workflow success | `Workflow {name} success on {owner/repo}` | success | Run URL |
| Workflow failure | `Workflow {name} failure on {owner/repo}` | error | Run URL |
| Workflow cancelled | `Workflow {name} cancelled on {owner/repo}` | warning | Run URL |
| Issue comment created | `Comment on #{n}: {issue title} on {owner/repo}` | info, or `<!-- homerun2:severity=… -->` | Comment URL |

All watcher-generated messages include:

- `system`: `homerun2-git-pitcher`
- `author`: GitHub actor login
- `tags`: `github,{event_type},{owner/repo}`, then `key=value` tags such as `repo=`, `number=`, `action=`, `head=`, `base=`, `actor=`, `title=` (see below)
- `url`: the event's page on GitHub (`html_url`)

### Tags

The plain tags `github,{event_type},{owner/repo}` come first and never change, so filters such as `tags_contain: [pull_request]` keep matching; title and message are unchanged too (`message_contains: ["PR merged by"]`). After them (and after any `homerun2:tags=` marker tags of a comment) come `key=value` tags. Empty values are left out; a comma in a value becomes `_` (`;` in `title`).

| GitHub Event | key=value tags |
|-------------|----------------|
| Push | `repo`, `branch`, `actor` |
| Pull Request | `repo`, `number`, `action` (`opened`, `reopened`, `ready_for_review`, `closed`, `merged`; a closed and merged PR is `merged`), `head`, `base`, `actor` (the merger for `merged`), `title` (the bare PR title; a comma becomes `;`, whitespace trimmed, left out when empty) |
| Release | `repo`, `action`, `tag`, `actor` |
| Workflow Run | `repo`, `workflow`, `conclusion`, `branch`, `actor` |
| Issue Comment | `repo`, `number`, `action`, `actor` |
| Issues | `repo`, `number`, `action`, `actor` |

A merged pull request, for example:

```json
{
  "title": "PR #4404: feat(platform-sthings): route the catcher's Teams outputs to their channels (merged) on stuttgart-things/stuttgart-things",
  "message": "PR merged by patrick-hermann-sva: feat/platform-sthings-teams-routing → main",
  "tags": "github,pull_request,stuttgart-things/stuttgart-things,repo=stuttgart-things/stuttgart-things,number=4404,action=merged,head=feat/platform-sthings-teams-routing,base=main,actor=patrick-hermann-sva,title=feat(platform-sthings): route the catcher's Teams outputs to their channels",
  "url": "https://github.com/stuttgart-things/stuttgart-things/pull/4404"
}
```
