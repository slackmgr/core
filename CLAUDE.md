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
go test -v ./manager/internal/models/
```

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
- `manager/internal/models/` - Core data structures (Issue, Alert, Command, IssueCollection)
- `manager/internal/slack/` - Slack integration layer with controllers for different event types
- `manager/internal/slack/views/` - Slack UI components (modals, message blocks)
- `config/` - Configuration structures for API and Manager
- `internal/` - Shared internal utilities (cache, encryption, channel summary)
- `internal/slackapi/` - Low-level Slack API wrapper with caching and rate limit handling

## Key Dependencies

- `github.com/peteraglen/slack-manager-common` - Common interfaces (`Logger`, `DB`, `Metrics`) shared across services
- `github.com/gin-gonic/gin` - HTTP web framework for REST API
- `github.com/slack-go/slack` - Slack API client and Socket Mode
- `github.com/eko/gocache` - Caching abstraction with Redis backend
- `github.com/bsm/redislock` - Distributed locking for multi-instance deployments
- `github.com/stretchr/testify` - Testing assertions and mocks

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
- Use **ShouldBindJSON** for request validation.
- Implement a global error handling strategy.
- Keep handlers thin; delegate business logic to the `service` layer.

#### 2. Logging
- All logging via injected Logger instances implementing the `common.Logger` interface (from `slack-manager-common` package).
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
- Every new function has a corresponding test in a `_test.go` file
- Tests use `t.Parallel()` where appropriate
- External dependencies are mocked via interfaces
- Test suite covers "Happy Path" AND "Edge Cases" (nil pointers, empty strings, timeouts)
- `go test -race ./...` passes
- `gosec` scan passes with no issues
- `golangci-lint` passes with no issues

#### Gin API Checklist
- Status codes are semantic (e.g., `201 Created` for POST, `400 Bad Request` for validation errors)
- All inputs validated using `binding:"required"` or custom validators
- API returns a consistent JSON error structure
- Graceful Shutdown is implemented for the HTTP server

#### Logging Checklist
- Logger is passed via context or dependency injection
- Logs use the correct level (`Debug` for noise, `Warn` for retries, `Error` for failures)
- Sensitive fields (passwords, PII) are masked or excluded from logs

#### Makefile Checklist
- `make init`: Runs `go mod tidy`
- `make test`: Runs all tests with coverage reports, as well as `gosec`, `go fmt`, and `go vet`
- `make lint`: Runs `golangci-lint`

### Idiomatic Go Rules

1. **Errors are values:** Don't panic. Handle errors explicitly.
2. **Interfaces:** Keep them small (usually 1-3 methods). "Accept interfaces, return structs."
3. **Concurrency:** Don't use channels where a simple Mutex suffices, but prefer channels for communication.
4. **Naming:** Use `camelCase`. Keep variable names short if their scope is small (e.g., `r` for `Reader`).
