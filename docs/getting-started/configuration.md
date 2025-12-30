# Configuration

Slack Manager uses two main configuration structures: `APIConfig` for the API server and `APISettings` for routing rules.

## API Configuration

```go
cfg := config.NewDefaultAPIConfig()
```

### Slack Client Settings

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `BotToken` | string | - | Slack Bot User OAuth Token (`xoxb-...`) |
| `AppToken` | string | - | Slack App-Level Token (`xapp-...`) |
| `MaxRateLimitErrorWaitTimeSeconds` | int | 120 | Max wait time for rate limit errors |
| `MaxTransientErrorWaitTimeSeconds` | int | 30 | Max wait time for transient errors |
| `MaxFatalErrorWaitTimeSeconds` | int | 30 | Max wait time for fatal errors |

### Rate Limiting

```go
cfg.RateLimit.AlertsPerSecond = 1.0    // Alerts per second per channel
cfg.RateLimit.AllowedBurst = 10        // Max burst size
cfg.RateLimit.MaxAttempts = 3          // Max retry attempts
cfg.RateLimit.MaxWaitPerAttemptSeconds = 10
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `AlertsPerSecond` | float64 | 1.0 | Token bucket refill rate |
| `AllowedBurst` | int | 10 | Max alerts in a single request |
| `MaxAttempts` | int | 3 | Retry attempts before failing |
| `MaxWaitPerAttemptSeconds` | int | 10 | Timeout per attempt |

### Other Settings

```go
cfg.RestPort = "8080"                    // API server port
cfg.MaxUsersInAlertChannel = 1000        // Max users in alert channel
cfg.ErrorReportChannelID = "C0123456789" // Channel for error reports
cfg.EncryptionKey = "32-byte-key-here"   // Webhook payload encryption
cfg.CacheKeyPrefix = "slack-manager"     // Cache key prefix
```

## API Settings (Routing Rules)

Routing rules determine which Slack channel receives alerts based on the alert's `routeKey` field.

### Rule Structure

```go
settings := &config.APISettings{
    RoutingRules: []*config.RoutingRule{
        {
            Name:        "production-alerts",
            Description: "Route production alerts to #prod-alerts",
            Channel:     "C0123456789",
            Equals:      []string{"production", "prod"},
        },
    },
}
```

### Matching Options

Rules are evaluated in order. The first matching rule wins.

#### Exact Match

```go
{
    Name:    "exact-match",
    Channel: "C0123456789",
    Equals:  []string{"my-service", "another-service"},
}
```

Matches alerts where `routeKey` equals "my-service" or "another-service" (case-insensitive).

#### Prefix Match

```go
{
    Name:      "prefix-match",
    Channel:   "C0123456789",
    HasPrefix: []string{"prod-", "production-"},
}
```

Matches alerts where `routeKey` starts with "prod-" or "production-".

#### Regex Match

```go
{
    Name:         "regex-match",
    Channel:      "C0123456789",
    MatchesRegex: []string{"^web-server-\\d+$"},
}
```

Matches alerts where `routeKey` matches the regex pattern.

#### Match All (Fallback)

```go
{
    Name:     "catch-all",
    Channel:  "C0123456789",
    MatchAll: true,
}
```

Matches all alerts. Place this rule last as a fallback.

### Alert Type Filtering

Rules can optionally filter by alert type:

```go
{
    Name:      "prometheus-only",
    Channel:   "C0123456789",
    AlertType: "prometheus",
    MatchAll:  true,
}
```

## Environment Variables

Common configuration via environment variables:

```bash
export SLACK_BOT_TOKEN="xoxb-your-token"
export SLACK_APP_TOKEN="xapp-your-token"
export API_PORT="8080"
export ERROR_CHANNEL="C0123456789"
export ENCRYPTION_KEY="your-32-byte-encryption-key!!"
```

## Example: Complete Configuration

```go
package main

import (
    "github.com/peteraglen/slack-manager/config"
)

func createConfig() (*config.APIConfig, *config.APISettings) {
    cfg := config.NewDefaultAPIConfig()

    // Slack credentials
    cfg.SlackClient.BotToken = os.Getenv("SLACK_BOT_TOKEN")
    cfg.SlackClient.AppToken = os.Getenv("SLACK_APP_TOKEN")

    // Rate limiting
    cfg.RateLimit.AlertsPerSecond = 2.0
    cfg.RateLimit.AllowedBurst = 20

    // Error reporting
    cfg.ErrorReportChannelID = "C0ERROR123"

    // Routing rules
    settings := &config.APISettings{
        RoutingRules: []*config.RoutingRule{
            {
                Name:      "production",
                Channel:   "C0PROD123",
                HasPrefix: []string{"prod-"},
            },
            {
                Name:      "staging",
                Channel:   "C0STAGE123",
                HasPrefix: []string{"staging-", "stage-"},
            },
            {
                Name:     "default",
                Channel:  "C0DEFAULT",
                MatchAll: true,
            },
        },
    }

    return cfg, settings
}
```
