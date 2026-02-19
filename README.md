# core

[![Go Reference](https://pkg.go.dev/badge/github.com/slackmgr/core.svg)](https://pkg.go.dev/github.com/slackmgr/core)
[![Go Report Card](https://goreportcard.com/badge/github.com/slackmgr/core)](https://goreportcard.com/report/github.com/slackmgr/core)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![CI](https://github.com/slackmgr/core/workflows/CI/badge.svg)](https://github.com/slackmgr/core/actions)

The core implementation library for Slack Manager. Provides two importable Go packages:

- **`restapi`** — HTTP server that accepts alerts and enqueues them for processing
- **`manager`** — Alert processor that manages issue lifecycle in Slack channels

This library has no `main.go` entry point. It is designed to be embedded in a host application that wires up the required dependencies: database, message queue, logging, and metrics.

## When to Use This Library

Import this library only if you are **building a Slack Manager host application** — a program that runs the alert ingestion server and/or the Slack manager service.

Use [go-client](https://github.com/slackmgr/go-client) to send alerts to a running Slack Manager.

## Architecture

```
External Systems → API Server → Alert Queue → Manager → Coordinator → Channel Manager → Database + Slack
                                                                   ↑
Slack Events ────────────────────────────────→ Socket Mode ────────┘
```

### API Server (`restapi`)

- REST server on port 8080 accepting alerts
- Endpoints: `POST /alert[s]`, `POST /prometheus-alert`, `GET /mappings`, `GET /channels`, `GET /ping`
- Per-channel rate limiting
- Validates and enqueues alerts for the manager to process

### Manager (`manager`)

- Dequeues and processes alerts from the alert queue
- Groups correlated alerts into "issues" per Slack channel
- Manages issue lifecycle: creation, grouping, escalation, resolution, archival
- Handles Slack interactions (reactions, slash commands, modals) via Socket Mode
- Supports distributed deployments via Redis-backed channel locking

## Installation

```bash
go get github.com/slackmgr/core
```

Requires Go 1.25+.

## Quick Start

```go
package main

import (
    "context"
    "log"

    redis "github.com/redis/go-redis/v9"
    "github.com/slackmgr/core/config"
    "github.com/slackmgr/core/manager"
    "github.com/slackmgr/core/restapi"
    "golang.org/x/sync/errgroup"
)

func main() {
    ctx := context.Background()

    // Your implementations of types.Logger, types.Metrics, and types.DB
    logger := ...
    metrics := ...
    db := ...

    redisClient := redis.NewClient(&redis.Options{Addr: "localhost:6379"})

    locker := manager.NewRedisChannelLocker(redisClient)
    alertQueue, _ := manager.NewRedisFifoQueue(redisClient, locker, "alerts", logger).Init()
    commandQueue, _ := manager.NewRedisFifoQueue(redisClient, locker, "commands", logger).Init()

    apiCfg := &config.APIConfig{
        RestPort:    "8080",
        SlackClient: &config.SlackClientConfig{BotToken: "xoxb-...", AppToken: "xapp-..."},
    }

    managerCfg := &config.ManagerConfig{
        SlackClient: &config.SlackClientConfig{BotToken: "xoxb-...", AppToken: "xapp-..."},
    }

    server := restapi.New(alertProducer, nil, logger, metrics, apiCfg, nil)
    mgr := manager.New(db, alertQueue, commandQueue, nil, locker, logger, metrics, managerCfg, nil)

    g, ctx := errgroup.WithContext(ctx)
    g.Go(func() error { return server.Run(ctx) })
    g.Go(func() error { return mgr.Run(ctx) })

    if err := g.Wait(); err != nil {
        log.Fatal(err)
    }
}
```

## Provided Implementations

### Redis FIFO Queue

Backed by Redis Streams with per-channel ordering via distributed locks. Supports single-node, Sentinel, and cluster Redis clients.

```go
// Full queue — use in the manager (and the API, if running in the same application)
locker := manager.NewRedisChannelLocker(redisClient)
queue, err := manager.NewRedisFifoQueue(redisClient, locker, "alerts", logger,
    manager.WithKeyPrefix("myapp:queue"),
    manager.WithConsumerGroup("myapp"),
).Init()

// Write-only producer — use when running the API in a separate application
producer, err := manager.NewRedisFifoQueueProducer(redisClient, "alerts", logger,
    manager.WithKeyPrefix("myapp:queue"),
    manager.WithConsumerGroup("myapp"),
).Init()
```

**Options:**

| Option | Default | Description |
|--------|---------|-------------|
| `WithKeyPrefix(string)` | `"slack-manager:queue"` | Redis key prefix |
| `WithConsumerGroup(string)` | `"slack-manager"` | Redis consumer group name — all instances sharing a queue must use the same value |
| `WithPollInterval(time.Duration)` | `2s` | How long to wait between poll cycles when idle |
| `WithMaxStreamLength(int64)` | `10000` | Approximate maximum messages per stream before trimming |
| `WithStreamRefreshInterval(time.Duration)` | `30s` | How often to check for new streams |
| `WithClaimMinIdleTime(time.Duration)` | `120s` | Minimum idle time before a pending message can be claimed by another consumer |
| `WithLockTTL(time.Duration)` | `140s` | Per-stream lock TTL; must be greater than `WithClaimMinIdleTime` |
| `WithStreamInactivityTimeout(time.Duration)` | `48h` | How long an empty stream is kept before cleanup; `0` disables cleanup |

### Channel Lockers

```go
// Redis-backed — required for multi-instance / Kubernetes deployments
locker := manager.NewRedisChannelLocker(redisClient)

// No-op — suitable for single-instance deployments only
locker := &manager.NoopChannelLocker{}
```

## Slack App Setup

A `manifest.json` is included in this repository. Use it to create or configure your Slack app at [api.slack.com/apps](https://api.slack.com/apps).

The app requires the following bot token scopes: `app_mentions:read`, `channels:history`, `channels:join`, `channels:manage`, `channels:read`, `chat:write`, `chat:write.customize`, `commands`, `emoji:read`, `groups:history`, `groups:read`, `groups:write`, `incoming-webhook`, `reactions:read`, `reactions:write`, `users:read`, `users:read.email`, `usergroups:read`.

Socket Mode must be enabled. Two tokens are required:

- **Bot token** (`xoxb-...`) — used for all Slack API calls
- **App-level token** (`xapp-...`) with the `connections:write` scope — used for Socket Mode

## License

This project is licensed under the MIT License — see the [LICENSE](LICENSE) file for details.

Copyright (c) 2026 Peter Aglen
