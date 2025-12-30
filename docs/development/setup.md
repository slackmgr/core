# Development Setup

Set up your development environment for contributing to Slack Manager.

## Prerequisites

- Go 1.21 or later
- Git
- Make
- golangci-lint (for linting)

## Clone the Repository

```bash
git clone https://github.com/peteraglen/slack-manager.git
cd slack-manager
```

## Install Dependencies

```bash
make init
# or
go mod tidy
```

## Project Structure

```
slack-manager/
├── api/                        # REST API server
│   ├── server.go              # Server setup and middleware
│   ├── handle_alert.go        # Alert endpoints
│   ├── handle_prometheus_webhook.go
│   ├── handlers_test.go       # HTTP handler tests
│   └── *_internal_test.go     # Internal unit tests
├── manager/                    # Alert processing
│   ├── manager.go
│   └── internal/
│       ├── models/            # Data structures
│       └── slack/             # Slack integration
├── config/                     # Configuration
├── internal/
│   ├── slackapi/              # Slack API wrapper
│   └── encryption.go          # Webhook encryption
├── docs/                       # Documentation (MkDocs)
├── Makefile                    # Build automation
└── go.mod                      # Go modules
```

## Makefile Targets

```bash
make init      # Download dependencies (go mod tidy)
make test      # Run full test suite
make lint      # Run linter (golangci-lint)
```

## Running Tests

### All Tests

```bash
make test
# or
go test ./...
```

### Specific Package

```bash
go test -v ./api/...
go test -v ./manager/...
go test -v ./config/...
```

### Single Test

```bash
go test -v -run TestPingEndpoint ./api/...
```

### With Coverage

```bash
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### Race Detection

```bash
go test -race ./...
```

## Code Style

### Formatting

```bash
go fmt ./...
```

### Linting

```bash
make lint
# or
golangci-lint run --fix
```

### Linter Configuration

See `.golangci.yaml` for linter settings.

## Testing Conventions

### File Naming

| File Pattern | Purpose |
|--------------|---------|
| `*_test.go` | External tests (black-box) |
| `*_internal_test.go` | Internal tests (white-box) |

### Test Structure

```go
func TestFunctionName(t *testing.T) {
    t.Parallel()

    t.Run("descriptive test case name", func(t *testing.T) {
        t.Parallel()

        // Arrange
        server, queue, mockProvider := testServer(t)

        // Act
        result := server.SomeMethod()

        // Assert
        assert.Equal(t, expected, result)
    })
}
```

### Mocking

Use interfaces for dependencies and create mock implementations:

```go
// Interface
type FifoQueueProducer interface {
    Send(ctx context.Context, channelID, dedupID, body string) error
}

// Mock
type mockQueue struct {
    messages []queueMessage
    sendErr  error
}

func (m *mockQueue) Send(ctx context.Context, channelID, dedupID, body string) error {
    if m.sendErr != nil {
        return m.sendErr
    }
    m.messages = append(m.messages, queueMessage{channelID, dedupID, body})
    return nil
}
```

## IDE Setup

### VS Code

Recommended extensions:

- Go (official)
- Go Test Explorer
- EditorConfig

`.vscode/settings.json`:
```json
{
  "go.lintTool": "golangci-lint",
  "go.lintFlags": ["--fast"],
  "go.testFlags": ["-v", "-race"],
  "editor.formatOnSave": true
}
```

### GoLand

- Enable "Go Modules Integration"
- Set "golangci-lint" as the linter
- Enable "Format on Save"

## Documentation

### MkDocs Setup

```bash
pip install mkdocs-material mkdocstrings
```

### Serve Locally

```bash
mkdocs serve
```

Open http://localhost:8000

### Build Static Site

```bash
mkdocs build
```

Output is in `site/` directory.

## Debugging

### Logging

Set log level via environment:

```bash
export LOG_LEVEL=debug
```

### Request Debugging

Enable debug logging to see request bodies:

```go
cfg.Debug = true
```

### Slack API Debugging

Use the `/alerts-test` endpoint to test without sending to Slack:

```bash
curl -X POST http://localhost:8080/alerts-test/C0123456789 \
  -d '{"header": "Test"}'
```
