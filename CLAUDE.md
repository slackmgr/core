# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**slack-manager** is a Go-based Slack bot/application that manages alerts and issues within Slack channels. It receives alerts from external monitoring systems (Prometheus, REST APIs), groups correlated alerts into "issues", and manages issue lifecycle through Slack reactions and interactions.

The code in this repo has no `main.go` files; instead, it is structured as importable libraries for the API server and the Manager service. The system relies on concurrency patterns, message queues, and Slack's Socket Mode for real-time event handling.

## Build and Test Commands

```bash
make init              # go mod tidy - download/clean dependencies
make test              # Full test suite: gosec, go fmt, go test -race ./..., go vet
make lint              # golangci-lint run
make lint-fix          # golangci-lint run --fix
make test-integration  # Integration tests with build tag

# Run a single test
go test -v -run TestName ./path/to/package

# Run tests for a specific package
go test -v ./internal/models/
```

## Code Quality Requirements

**CRITICAL:** Before committing any changes, you MUST ensure both `make test` and `make lint` pass without errors. This applies to ALL changes, regardless of who made them (human, Claude, or other tools/linters).

```bash
# Always run before committing:
make test    # Must pass: gosec, go fmt, go test (with race detector), go vet
make lint    # Must pass: golangci-lint with zero issues
```

If either command fails:
1. Fix all reported issues
2. Re-run both commands to verify
3. Only commit after both pass

This ensures code quality, prevents broken releases, and maintains consistency across the codebase.

## Keeping README.md in Sync

After every code change, check whether `README.md` needs updating. The README is the public-facing documentation and must always reflect the actual code.

## Metrics and Grafana Dashboard

**CRITICAL:** The Grafana dashboard `dashboards/slackmgr.json` must always match the metrics the code emits. **Every change to a metric MUST update the dashboard in the same commit.** A metric change without the matching dashboard change is incomplete, just like a code change without tests.

This applies to every metric registered via `RegisterCounter`, `RegisterGauge` or `RegisterHistogram` (in `manager/`, `restapi/`, `insights/` and `internal/`):

- **Added metric:** add a panel for it, in the matching row (or a new row). Do not leave new metrics off the dashboard
- **Renamed metric, or changed type, labels or label values:** update every query, legend, override and panel description that refers to it
- **Removed metric:** remove or rewrite every panel and query that uses it. No panel may query a metric that no longer exists
- **Changed meaning or buckets:** update panel descriptions, units and thresholds accordingly

Dashboard conventions:
- Queries use the default `slackmgr_` prefix (`config.DefaultMetricsPrefix`) and the `${datasource}` variable, and must work across multiple instances (aggregate with `sum`/`max`, not per-instance series)
- New panels go on a single line each (like the most recent panels), with a unique `id` and a non-overlapping `gridPos`. Shift the `y` of panels below when inserting
- Bump the dashboard `version`
- Verify before committing: the file is valid JSON, panel IDs are unique, and every metric name in the code appears in the dashboard (and vice versa)
- Mention dashboard changes in `CHANGELOG.md`

## Examples Repo

Do **not** modify files under `../examples/` unless explicitly asked to do so. They are updated independently.

## Tagging and Releases

### Process

1. **Update `CHANGELOG.md` first** — this is MANDATORY before creating any tag.
   - Review every commit since the last tagged commit: `git log <last-tag>..HEAD --oneline`
   - Every commit MUST be considered and represented under the correct section (`Added`, `Changed`, `Fixed`, `Removed`)
   - Add the new version section above `[Unreleased]` with today's date
   - Update the comparison links at the bottom of the file

2. **Commit the changelog:**
   ```bash
   git add CHANGELOG.md
   git commit -m "Update CHANGELOG for vX.Y.Z"
   ```

3. **Create and push the tag:**
   ```bash
   git tag vX.Y.Z
   git push origin main
   git push origin vX.Y.Z
   ```

4. **Create the GitHub release:**
   ```bash
   gh release create vX.Y.Z --repo slackmgr/core --title "vX.Y.Z" --notes "..."
   ```
   Use the same content as the changelog entry for the release notes.

### Versioning

Follows [Semantic Versioning](https://semver.org/):
- **Patch** (`Z`): bug fixes, CI/infra changes, documentation updates
- **Minor** (`Y`): new backwards-compatible features or functionality
- **Major** (`X`): breaking changes to the public API

### Rules

- **NEVER** create a tag without updating `CHANGELOG.md` first
- **ALWAYS** review all commits since the last tag — do not rely on memory or summaries

## Architecture

The system runs as two main services that communicate via message queues:

### API Server (`restapi/`)
- REST server on port 8080 accepting alerts
- Endpoints: `POST /alert`, `POST /prometheus-alert`, `GET /mappings`, `GET /channels`, `GET /ping`
- Applies rate limiting per Slack channel
- Enqueues alerts for processing

### Manager (`manager/`)
- Processes alerts and manages issue lifecycle
- Runs multiple concurrent goroutines:
  - Queue consumers (alerts and commands)
  - Message processor → Coordinator → Channel Managers
  - Slack Socket Mode client for real-time events
- One `channelManager` instance per Slack channel handles issue creation, grouping, state transitions, and archival
- Uses Redis for caching
- Uses distributed locks to prevent race conditions when multiple manager instances run (for example, in Kubernetes)

### Data Flow
```
External Systems → API Server → Alert Queue → Manager → Coordinator → Channel Manager → Database + Slack
                                                                    ↑
Slack Events ─────────────────────────────────────→ Socket Mode ────┘
```

### Key Abstractions

Both services are designed as importable libraries (no `main.go` in repo):

```go
// API Server
server := restapi.New(alertQueue, cacheStore, logger, metrics, apiCfg, apiSettings)
server.Run(ctx)

// Manager
manager := manager.New(db, alertQueue, commandQueue, cacheStore, channelLocker, logger, metrics, managerCfg, managerSettings)
manager.Run(ctx)
```

## Key Directories

- `restapi/` - REST API server and handlers
- `manager/` - Alert processing and issue management orchestration
- `internal/models/` - Core data structures (Issue, Alert, Command, IssueCollection), shared by `manager` and `insights`
- `manager/internal/slack/` - Slack integration layer with controllers for different event types
- `manager/internal/slack/views/` - Slack UI components (modals, message blocks)
- `insights/` - Insights `Recorder` (async, isolated event recording used by manager and API) and read `Service` (dashboard queries on a `types.InsightsReader`)
- `config/` - Configuration structures for API and Manager
- `internal/` - Shared internal utilities (cache, encryption, channel summary, low-level Slack API wrapper)

## Key Dependencies

- `github.com/slackmgr/types` - Common interfaces (`Logger`, `DB`, `Metrics`) shared across services
- `github.com/gin-gonic/gin` - HTTP web framework for REST API
- `github.com/slack-go/slack` - Slack API client and Socket Mode
- `github.com/eko/gocache` - Caching abstraction with Redis backend
- `github.com/bsm/redislock` - Distributed locking for multi-instance deployments
- `github.com/stretchr/testify` - Testing assertions and mocks

## Insights

- Emit sites must never block, allocate heavily, or do extra I/O: guard with `recorder.Enabled()`, build events only from in-memory data (`models.Issue.InsightsEvent`, `models.Alert.InsightsEvent`), and call `Record` (non-blocking). A nil `*insights.Recorder` is valid.
- The recorder sink is discovered on the raw DB in `Manager.Run`, before the DB cache middleware wraps it.
- Event types, reasons and value keys live in `github.com/slackmgr/types` (`InsightsEvent*`, `InsightsReason*`, `InsightsValue*`); add new ones there first.

## Concurrency Patterns

- `golang.org/x/sync/errgroup` for goroutine management
- Channels for inter-stage message passing
- Mutexes protect shared state in channel managers, rate limiters
- Per-channel token bucket rate limiters

## Testing

- Uses `github.com/stretchr/testify` for assertions
- Internal unit tests named `*_internal_test.go`
- 5-second timeout on all tests
- Security scanning via gosec included in test suite

## Agent Instructions: Go Development Standards

You are an expert Go Backend Engineer. Your goal is to produce performant, maintainable, and idiomatic Go code using **Gin** for APIs, **Zerolog** for logging, and **TDD** (Test-Driven Development) as the primary workflow.

### Tech Stack Specifics

#### 1. REST API (Gin)
- Use **Middleware** for cross-cutting concerns (Auth, Recovery, Logging).
- Implement a global error handling strategy.
- Keep handlers thin; delegate business logic to the `service` layer.

#### 2. Logging
- All logging via injected Logger instance implementing the `types.Logger` interface (from `types` package).
- Use **Structured Logging** (JSON).
- Never use `fmt.Printf` or standard `log`.
- Include context when applicable: `logger.WithField("user_id", id).Info("action")`.

#### 3. Test-Driven Development (TDD)
- Write a failing test **before** writing implementation code.
- Use **Table-Driven Tests** for complex logic.
- Use `testify/assert`, `testify/require` and `testify/mock` for readability.
- Mock external dependencies (DB, APIs) using interfaces.

### Quality Checklists

#### Testing & TDD Checklist
- Every new function has a corresponding test in a `_test.go` file.
- Tests use `t.Parallel()` where appropriate.
- External dependencies are mocked via interfaces.
- Test suite covers "Happy Path" AND "Edge Cases" (nil pointers, empty strings, timeouts).
- `go test -race ./...` passes.
- `gosec` scan passes with no issues.
- `golangci-lint` passes with no issues.

#### Gin API Checklist
- Status codes are semantic (e.g., `201 Created` for POST, `400 Bad Request` for validation errors).
- All inputs validated using `binding:"required"` or custom validators.
- API returns a consistent JSON error structure.
- Graceful Shutdown is implemented for the HTTP server.

#### Logging Checklist
- Logger is passed via context or dependency injection.
- Logs use the correct level (`Debug` for noise, `Warn` for retries, `Error` for failures).
- Sensitive fields (passwords, PII) are masked or excluded from logs.

#### Makefile Checklist
- `make init`: Runs `go mod tidy`
- `make test`: Runs all tests with coverage reports, as well as `gosec`, `go fmt`, and `go vet`
- `make lint`: Runs `golangci-lint`, using the config file in the repo root.

### Idiomatic Go Rules

1. **Errors are values:** Don't panic. Handle errors explicitly.
2. **Interfaces:** Keep them small (usually 1-3 methods). "Accept interfaces, return structs."
3. **Concurrency:** Don't use channels where a simple Mutex suffices, but prefer channels for communication.
4. **Naming:** Use `camelCase`. Keep variable names short if their scope is small (e.g., `r` for `Reader`).
