# Quick Start

Get Slack Manager up and running in minutes.

## Prerequisites

- Go 1.21 or later
- A Slack workspace with admin access
- AWS credentials (for SQS and DynamoDB)

## Step 1: Create a Slack App

1. Go to [api.slack.com/apps](https://api.slack.com/apps) and click **Create New App**
2. Choose **From scratch** and give your app a name
3. Enable **Socket Mode** under Settings
4. Add the following **Bot Token Scopes** under OAuth & Permissions:
   - `channels:history`
   - `channels:read`
   - `chat:write`
   - `reactions:read`
   - `reactions:write`
   - `users:read`
5. Install the app to your workspace
6. Copy the **Bot User OAuth Token** (`xoxb-...`) and **App-Level Token** (`xapp-...`)

## Step 2: Install Slack Manager

```bash
go get github.com/peteraglen/slack-manager
```

## Step 3: Configure and Run

```go
package main

import (
    "context"
    "log"

    "github.com/peteraglen/slack-manager/api"
    "github.com/peteraglen/slack-manager/config"
)

func main() {
    ctx := context.Background()

    // Create configuration
    cfg := config.NewDefaultAPIConfig()
    cfg.SlackClient.BotToken = "xoxb-your-bot-token"
    cfg.SlackClient.AppToken = "xapp-your-app-token"

    // Create routing rules
    settings := &config.APISettings{
        RoutingRules: []*config.RoutingRule{
            {
                Name:     "default",
                Channel:  "C0123456789", // Your alert channel ID
                MatchAll: true,
            },
        },
    }

    // Initialize and run
    server := api.New(alertQueue, cacheStore, logger, metrics, cfg, settings)
    if err := server.Run(ctx); err != nil {
        log.Fatal(err)
    }
}
```

## Step 4: Send Your First Alert

```bash
curl -X POST http://localhost:8080/alert/C0123456789 \
  -H "Content-Type: application/json" \
  -d '{
    "header": ":rotating_light: High CPU Usage",
    "text": "Server web-01 CPU at 95%",
    "severity": "error",
    "correlationId": "cpu-alert-web-01"
  }'
```

You should see the alert appear in your Slack channel!

## Next Steps

- [Configure routing rules](configuration.md) to route alerts to different channels
- [Set up Prometheus integration](../api/prometheus.md) for AlertManager webhooks
- [Deploy to production](../operations/deployment.md) with proper infrastructure
