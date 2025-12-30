# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**slack-manager** is a Go-based Slack bot/application that manages alerts and issues within Slack channels. It receives alerts from external monitoring systems (Prometheus, REST APIs), groups correlated alerts into "issues", and manages issue lifecycle through Slack reactions and interactions.

## Build and Test Commands

```bash
make init      # go mod tidy - download/clean dependencies
make test      # Full test suite: gosec, go fmt, go test ./..., go vet
make lint      # golangci-lint run --fix

# Run a single test
go test -v -run TestName ./path/to/package

# Run tests for a specific package
go test -v ./manager/internal/models/
```

## Architecture

The system runs as two main services that communicate via message queues:

### API Server (`api/`)
- REST server on port 8080 accepting alerts
- Endpoints: `POST /alert`, `POST /prometheus-alert`, `GET /mappings`, `GET /channels`, `GET /ping`
- Applies per-channel rate limiting
- Enqueues alerts for processing

### Manager (`manager/`)
- Processes alerts and manages issue lifecycle
- Runs multiple concurrent goroutines:
  - Queue consumers (alerts and commands)
  - Message processor → Coordinator → Channel Managers
  - Slack Socket Mode client for real-time events
- One `channelManager` instance per Slack channel handles issue creation, grouping, state transitions, and archival

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
server := api.New(alertQueue, cacheStore, logger, metrics, apiConfig, apiSettings)
server.Run(ctx)

// Manager
manager := manager.New(db, alertQueue, commandQueue, cacheStore, logger, metrics, managerConfig, managerSettings)
manager.Run(ctx)
```

### Core Interfaces
- `FifoQueue` - Message queue abstraction (alert queue, command queue)
- `DB` - Data persistence for issues
- `WebhookHandler` - Custom webhook handling
- Shared library `github.com/peteraglen/slack-manager-common` provides `Logger`, `Metrics`, `FifoQueueItem`

## Key Directories

- `api/` - REST API server and handlers
- `manager/` - Alert processing and issue management orchestration
- `manager/internal/models/` - Core data structures (Issue, Alert, Command, IssueCollection)
- `manager/internal/slack/` - Slack integration layer with controllers for different event types
- `manager/internal/slack/views/` - Slack UI components (modals, message blocks)
- `config/` - Configuration structures for API and Manager
- `internal/slackapi/` - Low-level Slack API wrapper with caching and rate limit handling

## Concurrency Patterns

- `golang.org/x/sync/errgroup` for goroutine management
- Channels for inter-stage message passing
- Mutexes protect shared state in channel managers, rate limiters
- Per-channel token bucket rate limiters
- Message visibility extension prevents timeout during processing

## Testing

- Uses `github.com/stretchr/testify` for assertions
- Internal unit tests named `*_internal_test.go`
- 5-second timeout on all tests
- Security scanning via gosec included in test suite


This `agents.md` file is designed to be consumed by AI agents (like GitHub Copilot, Cursor, or custom GPTs) and developers to ensure consistent, high-quality Go development using your specific tech stack.

---

# 🤖 Agent Instructions: Go Development Standards

You are an expert Go Backend Engineer. Your goal is to produce performant, maintainable, and idiomatic Go code using **Gin** for APIs, **Zerolog** for logging, and **TDD** (Test-Driven Development) as the primary workflow.

## 🏗️ Project Architecture
Follow the Standard Go Project Layout:
- `/cmd`: Entry points (main.go).
- `/internal`: Private library code (business logic, repository, services).
- `/pkg`: Public library code (shared utilities).
- `/api`: OpenAPI/Swagger specs.
- `Makefile`: Essential automation tasks.

---

## 🛠️ Tech Stack Specifics

### 1. REST API (Gin)
- Use **Middleware** for cross-cutting concerns (Auth, Recovery, Logging).
- Use **ShouldBindJSON** for request validation.
- Implement a global error handling strategy.
- Keep handlers thin; delegate business logic to the `service` layer.

### 2. Logging (Zerolog)
- Use **Structured Logging** (JSON).
- Never use `fmt.Printf` or standard `log`.
- Include context: `logger.Info().Str("user_id", id).Msg("action")`.
- Set log level via environment variables (`DEBUG`, `INFO`, `ERROR`).

### 3. Test-Driven Development (TDD)
- Write a failing test **before** writing implementation code.
- Use **Table-Driven Tests** for complex logic.
- Use `testify/assert` and `testify/mock` for readability.
- Mock external dependencies (DB, APIs) using interfaces.

---

## ✅ Quality Checklists

### 🧪 Testing & TDD Checklist
- [ ] Does every new function have a corresponding test in a `_test.go` file?
- [ ] Are tests using `t.Parallel()` where appropriate?
- [ ] Are external dependencies mocked via interfaces?
- [ ] Does the test suite cover "Happy Path" AND "Edge Cases" (nil pointers, empty strings, timeouts)?
- [ ] Is `go test -race ./...` passing?

### 🌐 Gin API Checklist
- [ ] Are status codes semantic (e.g., `201 Created` for POST, `400 Bad Request` for validation errors)?
- [ ] Is there a request ID in the header for traceability?
- [ ] Are all inputs validated using `binding:"required"` or custom validators?
- [ ] Does the API return a consistent JSON error structure?
- [ ] Is Graceful Shutdown implemented for the HTTP server?

### 🪵 Zerolog Checklist
- [ ] Is the logger initialized in `main.go` and passed via context or dependency injection?
- [ ] Are logs using the correct level (`Debug` for noise, `Warn` for retries, `Error` for failures)?
- [ ] Are sensitive fields (passwords, PII) masked or excluded from logs?
- [ ] Is the `timestamp` field enabled?
- [ ] Are errors logged using `.Err(err)` to capture stack traces?

### 🛠️ Makefile Checklist
- [ ] `make build`: Compiles the binary to `bin/`.
- [ ] `make test`: Runs all tests with coverage reports.
- [ ] `make lint`: Runs `golangci-lint`.
- [ ] `make tidy`: Runs `go mod tidy` and `go mod vendor`.
- [ ] `make watch`: Uses `air` for live reloading during development.

---

## 📋 Idiomatic Go Rules
1. **Errors are values:** Don't panic. Handle errors explicitly.
2. **Interfaces:** Keep them small (usually 1-3 methods). "Accept interfaces, return structs."
3. **Concurrency:** Don't use channels where a simple Mutex suffices, but prefer channels for communication.
4. **Naming:** Use `camelCase`. Keep variable names short if their scope is small (e.g., `r` for `Reader`).

---

## 🚀 Example Makefile
```makefile
.PHONY: build test lint tidy run

BINARY_NAME=app

build:
	go build -o bin/$(BINARY_NAME) ./cmd/api

test:
	go test -v -race ./...

lint:
	golangci-lint run

tidy:
	go mod tidy

run:
	go run cmd/api/main.go
```

---

## 🧬 Sample TDD Workflow Snippet
1. **Red:** Write `internal/service/user_test.go` defining the expected behavior.
2. **Green:** Write the minimum code in `internal/service/user.go` to pass.
3. **Refactor:** Optimize code, ensure `zerolog` is capturing the process, and verify Gin handlers call the service correctly.

