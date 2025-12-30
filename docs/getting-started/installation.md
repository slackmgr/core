# Installation

## As a Go Library

Slack Manager is designed to be imported as a library into your own Go application:

```bash
go get github.com/peteraglen/slack-manager
```

### Minimum Go Version

Slack Manager requires **Go 1.21** or later.

## Dependencies

Slack Manager depends on several external services:

| Dependency | Purpose | Required |
|------------|---------|----------|
| Slack API | Bot communication | Yes |
| AWS SQS | Alert queue | Yes |
| AWS DynamoDB | Issue storage | Yes |
| Redis/Go-Cache | Caching | Optional |

## Building from Source

```bash
# Clone the repository
git clone https://github.com/peteraglen/slack-manager.git
cd slack-manager

# Download dependencies
make init

# Run tests
make test

# Run linter
make lint
```

## Project Structure

```
slack-manager/
├── api/                    # REST API server
│   ├── server.go          # Server initialization
│   ├── handle_alert.go    # Alert endpoint handlers
│   └── handle_prometheus_webhook.go
├── manager/               # Alert processing and issue management
│   ├── manager.go        # Manager initialization
│   └── internal/
│       ├── models/       # Data structures
│       └── slack/        # Slack integration
├── config/               # Configuration structures
├── internal/
│   └── slackapi/        # Slack API wrapper
└── docs/                # Documentation
```

## Verifying Installation

Run the test suite to verify everything is working:

```bash
go test ./...
```

Expected output:
```
ok      github.com/peteraglen/slack-manager/api         0.630s
ok      github.com/peteraglen/slack-manager/config      0.123s
ok      github.com/peteraglen/slack-manager/manager     0.456s
```
