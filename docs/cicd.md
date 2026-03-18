# CI/CD

## GitHub Actions workflows

| Workflow | File | Trigger | Description |
|----------|------|---------|-------------|
| CI - Dagger Build & Test | `build-test.yaml` | Push/PR to main | Lint + build + integration tests via Dagger |
| Lint Repository | `lint-repo.yaml` | Push/PR to main | Repository-level linting |
| Build, Push & Scan | `build-scan-image.yaml` | Push/PR to main | Build container image with ko, push to ghcr.io, Trivy scan |
| Release | `release.yaml` | After image build | semantic-release: changelog, GitHub release |
| Pages | `pages.yaml` | Push to main | Publish MkDocs documentation |

## Release process

Releases are fully automated via [semantic-release](https://semantic-release.gitbook.io/):

- `fix:` commits trigger a **patch** bump (e.g. 1.0.0 → 1.0.1)
- `feat:` commits trigger a **minor** bump (e.g. 1.0.0 → 1.1.0)
- `feat!:` or `BREAKING CHANGE:` trigger a **major** bump
- Each release publishes the container image to `ghcr.io`

### Manual release trigger

```bash
task trigger-release
```

## Dagger module

The Dagger module at `dagger/main.go` provides these functions:

| Function | Description |
|----------|-------------|
| `lint` | Run golangci-lint (excludes dagger/ directory) |
| `build` | Compile Go binary |
| `build-image` | Build container image with ko, optional push |
| `scan-image` | Trivy vulnerability scan |
| `build-and-test-binary` | Build + integration test with Redis |
| `smoke-test` | Test a deployed instance with health checks and messages |

## Taskfile commands

```bash
task lint                    # Run golangci-lint via Dagger
task build-test-binary       # Build + test with Redis via Dagger
task build-scan-image-ko     # Build, push, scan container image
task build-output-binary     # Build Go binary to /tmp/go/build
task run-local               # Run locally with file backend
task run-redis-as-service    # Start Redis via Dagger
task trigger-release         # Trigger GitHub Actions release
```

## Branch and commit conventions

### Branch naming

- `fix/<issue-number>-<short-description>` — bug fixes
- `feat/<issue-number>-<short-description>` — features
- `test/<issue-number>-<short-description>` — test-only changes

### Commit messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: add rate limit monitoring with backoff
fix: resolve golangci-lint v2 config
test: add dedup store persistence tests
chore: update dependencies
docs: rewrite deployment guide
```
