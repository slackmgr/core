# Testing Guide

Comprehensive guide to testing in Slack Manager.

## Test Structure

### Test Categories

| Category | Location | Purpose |
|----------|----------|---------|
| Unit Tests | `*_test.go` | Test individual functions |
| Internal Tests | `*_internal_test.go` | Test unexported functions |
| Integration Tests | External | Test with real dependencies |

### Test Naming

```go
// Function: processAlerts
// Test file: handle_alert_test.go or handle_alert_internal_test.go

func TestProcessAlerts(t *testing.T) {
    t.Run("valid alert is processed", func(t *testing.T) { ... })
    t.Run("empty alerts returns no content", func(t *testing.T) { ... })
    t.Run("invalid channel returns error", func(t *testing.T) { ... })
}
```

## Running Tests

### Basic Commands

```bash
# All tests
go test ./...

# Verbose output
go test -v ./...

# Specific package
go test -v ./api/...

# Single test
go test -v -run TestPingEndpoint ./api/...

# With race detection
go test -race ./...
```

### Coverage

```bash
# Generate coverage report
go test -coverprofile=coverage.out ./...

# View in terminal
go tool cover -func=coverage.out

# View in browser
go tool cover -html=coverage.out

# Coverage for specific package
go test -coverprofile=coverage.out ./api/...
go tool cover -func=coverage.out | grep "api/"
```

### Timeout

Tests have a 5-second timeout by default:

```bash
go test -timeout 30s ./...
```

## Writing Tests

### Test Helpers

Create reusable test fixtures:

```go
// testServer creates a minimal server for testing
func testServer(t *testing.T) (*Server, *mockQueue, *mockChannelInfoProvider) {
    t.Helper()

    queue := newMockQueue()
    logger := &mockLogger{}
    metrics := &common.NoopMetrics{}
    cfg := config.NewDefaultAPIConfig()
    settings := &config.APISettings{}

    server := New(queue, nil, logger, metrics, cfg, settings)
    mockProvider := newMockChannelInfoProvider()
    server.channelInfoSyncer = mockProvider

    return server, queue, mockProvider
}
```

### HTTP Handler Tests

Use `httptest` for testing handlers:

```go
func TestPingEndpoint(t *testing.T) {
    t.Parallel()

    server, _, _ := testServer(t)
    router := setupTestRouter(server)

    req := httptest.NewRequest(http.MethodGet, "/ping", nil)
    w := httptest.NewRecorder()

    router.ServeHTTP(w, req)

    assert.Equal(t, http.StatusOK, w.Code)
    assert.Equal(t, "pong", w.Body.String())
}
```

### Table-Driven Tests

Use table-driven tests for multiple cases:

```go
func TestValidateChannelInfo(t *testing.T) {
    t.Parallel()

    tests := []struct {
        name      string
        info      *channelInfo
        wantErr   bool
        errContains string
    }{
        {
            name: "valid channel",
            info: &channelInfo{
                ChannelExists:      true,
                ManagerIsInChannel: true,
                UserCount:          5,
            },
            wantErr: false,
        },
        {
            name: "channel not found",
            info: &channelInfo{
                ChannelExists: false,
            },
            wantErr:     true,
            errContains: "unable to find",
        },
        {
            name: "archived channel",
            info: &channelInfo{
                ChannelExists:     true,
                ChannelIsArchived: true,
            },
            wantErr:     true,
            errContains: "archived",
        },
    }

    for _, tt := range tests {
        tt := tt // Capture range variable
        t.Run(tt.name, func(t *testing.T) {
            t.Parallel()

            server, _, _ := testServer(t)
            err := server.validateChannelInfo("C123", tt.info)

            if tt.wantErr {
                require.Error(t, err)
                assert.Contains(t, err.Error(), tt.errContains)
            } else {
                require.NoError(t, err)
            }
        })
    }
}
```

## Mocking

### Mock Interfaces

```go
// Logger mock
type mockLogger struct{}

func (m *mockLogger) Debug(msg string)                         {}
func (m *mockLogger) Debugf(format string, args ...interface{}) {}
func (m *mockLogger) Info(msg string)                          {}
// ... other methods

func (m *mockLogger) WithField(key string, value interface{}) common.Logger {
    return m
}
```

### Mock Queue

```go
type mockQueue struct {
    mu       sync.Mutex
    messages []queueMessage
    sendErr  error
}

func (m *mockQueue) Send(ctx context.Context, channelID, dedupID, body string) error {
    if m.sendErr != nil {
        return m.sendErr
    }
    m.mu.Lock()
    defer m.mu.Unlock()
    m.messages = append(m.messages, queueMessage{channelID, dedupID, body})
    return nil
}
```

### Mock Channel Provider

```go
type mockChannelInfoProvider struct {
    channels     map[string]*channelInfo
    channelNames map[string]string
}

func (m *mockChannelInfoProvider) GetChannelInfo(ctx context.Context, channelID string) (*channelInfo, error) {
    if info, ok := m.channels[channelID]; ok {
        return info, nil
    }
    return &channelInfo{ChannelExists: false}, nil
}
```

## Assertions

Use `testify` for assertions:

```go
import (
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestExample(t *testing.T) {
    // assert - continues on failure
    assert.Equal(t, expected, actual)
    assert.NotNil(t, value)
    assert.Contains(t, "hello world", "world")
    assert.True(t, condition)
    assert.Len(t, slice, 3)
    assert.Empty(t, slice)

    // require - stops on failure
    require.NoError(t, err)
    require.NotNil(t, value)
}
```

## Best Practices

### Parallel Tests

Always use `t.Parallel()` for test isolation:

```go
func TestSomething(t *testing.T) {
    t.Parallel()

    t.Run("case 1", func(t *testing.T) {
        t.Parallel()
        // ...
    })
}
```

### Test Isolation

- Don't share state between tests
- Create fresh fixtures in each test
- Use `t.Helper()` in helper functions

### Coverage Goals

| Package | Target Coverage |
|---------|-----------------|
| `api/` | 80%+ |
| `config/` | 90%+ |
| `manager/` | 70%+ |

### What to Test

- Happy path
- Error conditions
- Edge cases (empty input, nil, zero values)
- Boundary conditions

### What Not to Test

- Third-party libraries
- Simple getters/setters
- Code requiring live external services (use integration tests)
