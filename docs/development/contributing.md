# Contributing Guide

Guidelines for contributing to Slack Manager.

## Getting Started

1. Fork the repository
2. Clone your fork
3. Set up the development environment (see [Setup](setup.md))

## Development Workflow

### 1. Create a Branch

```bash
git checkout -b feature/your-feature-name
# or
git checkout -b fix/issue-description
```

### 2. Make Changes

- Follow the code style guidelines
- Write tests for new functionality
- Update documentation if needed

### 3. Test Your Changes

```bash
# Run all tests
make test

# Run linter
make lint

# Check formatting
go fmt ./...
```

### 4. Commit Changes

Use clear, descriptive commit messages:

```bash
git commit -m "Add feature: description of change"
git commit -m "Fix: description of bug fixed"
git commit -m "Refactor: description of refactoring"
```

### 5. Submit Pull Request

- Push your branch to your fork
- Open a pull request against `main`
- Fill out the PR template
- Wait for review

## Code Style

### Go Style

- Follow [Effective Go](https://golang.org/doc/effective_go)
- Use `go fmt` for formatting
- Use `golangci-lint` for linting

### Naming Conventions

| Type | Convention | Example |
|------|------------|---------|
| Package | lowercase | `slackapi` |
| Interface | PascalCase | `FifoQueue` |
| Struct | PascalCase | `ChannelManager` |
| Function | PascalCase (exported) | `ProcessAlert` |
| Function | camelCase (unexported) | `handleMessage` |
| Variable | camelCase | `channelID` |
| Constant | PascalCase or ALL_CAPS | `MaxRetries` |

### Error Handling

```go
// Good: Handle errors explicitly
result, err := doSomething()
if err != nil {
    return fmt.Errorf("doing something: %w", err)
}

// Bad: Ignoring errors
result, _ := doSomething()
```

### Logging

Use structured logging with zerolog:

```go
// Good
logger.Info().
    Str("channel_id", channelID).
    Int("alert_count", len(alerts)).
    Msg("Processing alerts")

// Bad
fmt.Printf("Processing %d alerts for %s\n", len(alerts), channelID)
```

## Testing Guidelines

### Test File Naming

| Pattern | Purpose |
|---------|---------|
| `*_test.go` | External (black-box) tests |
| `*_internal_test.go` | Internal (white-box) tests |

### Test Structure

```go
func TestFunctionName(t *testing.T) {
    t.Parallel()

    t.Run("descriptive case name", func(t *testing.T) {
        t.Parallel()

        // Arrange
        input := setupTestData()

        // Act
        result := FunctionUnderTest(input)

        // Assert
        assert.Equal(t, expected, result)
    })
}
```

### Table-Driven Tests

```go
func TestValidation(t *testing.T) {
    t.Parallel()

    tests := []struct {
        name    string
        input   string
        wantErr bool
    }{
        {"valid input", "good", false},
        {"empty input", "", true},
        {"invalid chars", "bad!", true},
    }

    for _, tt := range tests {
        tt := tt
        t.Run(tt.name, func(t *testing.T) {
            t.Parallel()
            err := Validate(tt.input)
            if tt.wantErr {
                assert.Error(t, err)
            } else {
                assert.NoError(t, err)
            }
        })
    }
}
```

### Mocking

Use interfaces for dependencies:

```go
// Define interface
type AlertSender interface {
    Send(ctx context.Context, alert *Alert) error
}

// Create mock
type mockAlertSender struct {
    sendFunc func(ctx context.Context, alert *Alert) error
}

func (m *mockAlertSender) Send(ctx context.Context, alert *Alert) error {
    return m.sendFunc(ctx, alert)
}
```

## Pull Request Guidelines

### PR Checklist

- [ ] Tests pass (`make test`)
- [ ] Linter passes (`make lint`)
- [ ] Documentation updated (if applicable)
- [ ] Commit messages are clear
- [ ] PR description explains the change

### PR Description Template

```markdown
## Summary
Brief description of changes.

## Changes
- Change 1
- Change 2

## Testing
How was this tested?

## Related Issues
Fixes #123
```

## Reporting Issues

### Bug Reports

Include:

1. Go version (`go version`)
2. Steps to reproduce
3. Expected behavior
4. Actual behavior
5. Error messages/logs

### Feature Requests

Include:

1. Use case description
2. Proposed solution
3. Alternatives considered

## Questions?

- Open a GitHub issue
- Check existing documentation
