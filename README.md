# slackmgr/core

[![Go Reference](https://pkg.go.dev/badge/github.com/slackmgr/core.svg)](https://pkg.go.dev/github.com/slackmgr/core)
[![Go Report Card](https://goreportcard.com/badge/github.com/slackmgr/core)](https://goreportcard.com/report/github.com/slackmgr/core)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![CI](https://github.com/slackmgr/core/workflows/CI/badge.svg)](https://github.com/slackmgr/core/actions)

Slack Manager turns a Slack channel into a structured, low-noise incident surface. Instead of a flood of individual alert messages, it maintains one continuously-updated post per problem — tracking state, grouping correlated alerts, escalating when needed, and archiving automatically when an issue is resolved. Teams react with emojis to take action, and external systems connect via webhooks, all without leaving Slack.

This repository contains the core library: two importable Go packages (`restapi` and `manager`) that are embedded in a host application alongside your choice of database, message queue, logging, and metrics.

## When to Use This Library

Import this library only if you are **building a Slack Manager host application** — a program that runs the alert ingestion server and/or the Slack manager service.

Use [go-client](https://github.com/slackmgr/go-client) to send alerts to a running Slack Manager.

## Features

### Alerts and Issues

Plain Slack integrations produce a flood of individual messages during an incident — one per alert, no grouping, no history, no lifecycle. Slack Manager replaces that with a single, continuously-updated Slack post per problem. When an alert arrives, Slack Manager checks whether an issue with the same **correlation ID** already exists in the target channel. If one does, the existing post is updated in place with the latest information rather than a new message being created. No matter how many alerts fire for the same problem — whether it's ten or ten thousand — the channel shows exactly one post per issue, keeping things readable during incident storms and making it immediately clear what is still ongoing versus what has been resolved.

Correlation IDs can be set **explicitly** by the alert producer (for example, the name of a failing service or a specific incident identifier) or omitted to let Slack Manager generate one automatically from the alert's content. Explicit IDs are powerful: they allow alerts from entirely different tools, systems, and alert types to converge into a single issue, giving a unified view of a problem regardless of how many monitoring systems detected it.

### Severity Levels

Every alert carries a severity: **panic**, **error**, **warning**, **info**, or **resolved**. Severity is the primary signal Slack Manager uses to make decisions — it determines the status icon displayed on the Slack post, controls how issues are ordered within a channel, governs whether escalation rules can trigger, and influences archival behaviour. A resolved-severity alert closes an issue rather than updating it. An info-severity alert is treated as a one-off notification with no ongoing lifecycle, since there is nothing to follow up on.

### Severity-Based Reordering

In a busy Slack channel, the most critical issues should always be the most visible. Slack Manager automatically repositions issues by severity so that the highest-priority problems sit at the bottom of the channel — where fresh messages naturally appear and eyes naturally land. When an issue's severity changes, whether due to a new incoming alert or an escalation rule triggering, it is moved to the correct position automatically. During a large-scale incident with many simultaneously open issues, reordering is suspended once the number of open issues exceeds a configurable threshold, preventing the API call volume from compounding an already difficult situation.

### Emoji-Driven Issue Management

Users interact with issues by reacting with emojis directly on the Slack post — no external commands, no dashboards to open, no bots to address. The reaction is both the discovery mechanism and the action trigger: the person who spots the issue can act on it immediately, right where they see it. Available actions:

| Reaction | Action |
|----------|--------|
| `:white_check_mark:` | Mark the issue as resolved — the post updates to a resolved state and begins its archiving countdown |
| `:eyes:` | Mark yourself as investigating — your display name appears on the post so the team knows someone is looking at it |
| `:mask:` | Mute the issue — suppresses escalation notifications without closing or hiding the issue |
| `:firecracker:` | Terminate — archive the issue immediately, regardless of its state |
| `:information_source:` | Toggle the visibility of interactive action buttons on the post |

Removing a reaction reverses the action: unreacting `:white_check_mark:` reopens the issue, unreacting `:eyes:` removes your name, and so on. Resolve, mute, and terminate require admin rights; marking yourself as investigating is open to anyone in the channel. All emoji triggers are configurable, so teams can remap actions to whichever emojis fit their workflow.

### Alert Routing via Route Keys

Alert producers do not need to know Slack channel IDs — identifiers that are opaque, environment-specific, and painful to manage in configuration files. Instead, an alert can specify a **route key**: a human-readable, semantic identifier such as `"payments"`, `"infra/database"`, or `"europe-west-1"`. Slack Manager maps route keys to channels using a routing table configured on the server side, so channel assignments can be changed at any time without touching the alerting systems.

Routing rules support four match strategies, evaluated in priority order: exact match, prefix match, regex match, and a catch-all fallback. Rules can also be scoped to specific alert types, so the same route key can direct a `"security"` alert to a different channel than a `"metrics"` alert. The routing table is hot-reloaded every ten seconds without a restart.

### Automatic Issue Resolution

In most monitoring setups, someone has to manually close alerts once a problem is fixed. Slack Manager eliminates that toil: issues with follow-up enabled resolve themselves automatically after a configurable quiet period during which no new alerts arrive for that correlation ID. This keeps channels clean without requiring human intervention for issues that heal on their own.

When an issue auto-resolves, the outcome can be configured in two ways. Marking it as **resolved** signals that the problem went away cleanly. Marking it as **inconclusive** signals that it disappeared without anyone confirming whether it was genuinely fixed or simply stopped firing — useful for distinguishing real resolutions from silent failures. After resolution, the issue remains visible in the channel for a configurable archiving delay, giving the team a chance to review it. During that window, a new alert with the same correlation ID will reopen the issue rather than creating a duplicate.

### The `:status:` Keyword

Alert text often needs to reflect the current state of the issue, but alert producers have no way to know what state an issue will be in when the post is read. The `:status:` keyword bridges that gap. When `:status:` appears anywhere in an alert's header or body text, Slack Manager replaces it with a live emoji that reflects the issue's actual current state: `:scream:` for panic, `:x:` for error, `:warning:` for warning, `:white_check_mark:` when resolved, and so on. As the issue changes state — whether through a new alert, an emoji action, or escalation — the icon in the post updates automatically, providing an at-a-glance status without the alert producer having to manage it. 

### Issue Escalation

Not every alert gets the same sense of urgency over time. A warning that has been sitting unacknowledged for two hours probably deserves more attention than it got initially. Slack Manager's escalation rules let you codify that intuition: each alert can define up to three escalation points that trigger after configurable delays if the issue is still open.

Each escalation rule can independently take one or more actions: **raise the severity** (promoting the issue to a higher urgency level and repositioning it in the channel), **add Slack mentions** (notifying on-call engineers or teams directly within the post), or **move the issue to a different channel** (routing it to a dedicated incident channel when it crosses a threshold). Multiple rules on the same alert create a stepped escalation ladder — a warning might notify a team lead after 30 minutes and page the whole team after two hours, without any external tooling.

Muting an issue suppresses all further escalation, and escalation does not fire for resolved or archived issues. Repeated escalation mentions are also throttled to prevent the same people being notified twice within a short window.

### Interactive Webhooks (Actionable Buttons)

Slack Manager can embed up to five interactive buttons directly on an issue's Slack post. When clicked, a button triggers an outbound callback to a configurable target — by default an HTTP POST to any URL, but the delivery mechanism is fully extensible. The action happens without the user leaving Slack, and without them needing to know the URL or payload format.

Before sending, a button can optionally display a confirmation dialog, collect free-text input, or present checkboxes — allowing the webhook recipient to receive context from the user alongside the fixed payload (such as a reason for the action, or a selection from pre-defined options). Access to each button is independently controlled: buttons can be restricted to channel admins, global admins, or open to all channel members, and can be configured to appear only when the issue is open, only when it is resolved, or always.

Webhook dispatch is governed by the `manager.WebhookHandler` interface. Each handler declares which targets it claims via `ShouldHandleWebhook` and delivers the payload via `HandleWebhook`. Registered handlers are evaluated in order; the first match wins. The built-in HTTP handler is always available and claims any `https://` URL. Additional handlers can be registered with `mgr.RegisterWebhookHandler`. The `sqs` and `pubsub` plugins (see [Plugins](#plugins) below) each provide a handler that intercepts targets whose format matches their respective service — an SQS queue URL or a Pub/Sub topic name — and publishes the callback payload directly to that queue or topic instead of making an HTTP call. Any custom delivery target is supported by implementing the two-method interface.

Webhook payloads are AES-256 encrypted in transit between the API server and the Manager, so both components must share a common `EncryptionKey`. When no key is configured, the API rejects alerts that include webhooks with HTTP 400, preventing unencrypted payloads from reaching the queue.

### Notification Delay

Not every problem that fires an alert warrants a Slack notification. Transient infrastructure blips, brief overload spikes, and self-healing failures often resolve within seconds — but without a delay mechanism they would still generate posts that clutter the channel and erode trust in the alerting system.

Alerts can specify a notification delay: the Slack post will not be created until the delay elapses. If the issue resolves itself within that window — because a subsequent `resolved`-severity alert arrives — no Slack post is ever created. Only problems that remain open past the threshold surface in Slack, dramatically reducing noise for teams that operate in environments with frequent transient events. The delay is calculated from the time the issue is first created, so it remains consistent regardless of how many alerts arrive in the interim.

### Alert Deduplication and Noise Filtering

Beyond grouping alerts by correlation ID, individual alerts can carry a list of text patterns to filter out. If an incoming alert's text matches any of those patterns, the alert is silently discarded without updating or reopening the issue. This is useful for suppressing known-noisy variants of an alert — log noise at the tail end of a deployment, repeated low-signal warnings from a chatty dependency — while still allowing the main alert to flow through normally.

### Duplicate Post Prevention

Creating a Slack post can fail in a way where the post may still have been created, for example after a timeout, an HTTP 5xx response, or a Slack `fatal_error`/`internal_error`. Such posts are not retried. Instead, the issue is marked as having a pending post, and the post is looked up in the channel history (`conversations.history`) before a new one is created. If found, the post is adopted by the issue, and any duplicates are deleted. Duplicates are always deleted, even if they have thread replies, since a duplicate is never the issue's post and would otherwise be left untracked in the channel. If not found within 30 seconds, a new post is created. If the channel history cannot be read, a new post is created after 10 minutes. Issues with a pending post are not archived until the post is resolved, and deleting their post (e.g. to re-post with escalation mentions) or moving them to another channel is postponed until it is resolved.

The channel history is read once per channel for all pending posts in a channel update cycle, and the result is reused for 10 seconds by all lookups in the channel. A failed read is not retried for 30 seconds. If the read fails because the channel has too many messages since the start of the lookup window, only lookups with the same or a longer window wait for the retry, so that one old pending post does not block newer ones. A post is only assumed to not exist if the history was read after the 30 second grace period. A failed read only fails the issues with a pending post, and does not cut the channel update cycle short, unless the error affects all Slack calls (e.g. an invalid token). Rate limit, transient and `missing_scope` errors from the history read do not cut the cycle short. A failed database save before a post is created (see below) always cuts the cycle short.

The same mechanism covers crashes: before a post is created, the issue is saved to the database as pending (write-ahead). If the process crashes after the post is created, but before the issue is saved with the post ID, the post is found and adopted when the issue is next processed. Since the post call (including rate limit and transient error retries) may still be running when the write-ahead is saved, the pending time of a write-ahead is set to when the post may have been created at the latest: now, plus the HTTP timeout, plus the larger of `maxRateLimitErrorWaitTimeSeconds` and `maxTransientErrorWaitTimeSeconds` (140 seconds with the defaults). The grace period and the 10 minute limit are measured from that time. If the crash happened before the post was created, a new post is therefore created after the max call duration plus the 30 second grace period. This costs one extra database write per created post. Moved issues are stored in the target channel with a write-ahead pending post as well, so that if the target channel lock expires during the move, the target channel waits for (and adopts) the post created by the move, rather than creating a post of its own. Time spent waiting for a free Slack API concurrency slot is not covered. Once a moved issue is stored in the target channel, the move is complete: if the final save fails, the target channel finds and adopts the post.

The grace period is compared across manager instances (e.g. a moved issue is posted by the source channel's instance, and looked up by the target channel's instance), so the clocks of all manager instances must be synchronised, e.g. with NTP. A clock difference of more than 30 seconds can cause a post to be created again.

To make posts identifiable, every issue post carries [message metadata](https://docs.slack.dev/messaging/message-metadata) with event type `slackmgr_issue` (or `slackmgr_issue_deleted` for posts that were updated to look deleted) and the issue ID as payload. Message metadata is visible to all users and apps in the workspace, so it contains nothing but the opaque issue ID.

The `slack_pending_posts_total{result}` metric counts posts with an unknown outcome (`pending`) and how they were resolved (`adopted`, `not_found`, `expired`, `duplicate_deleted`). `lookup_failed` counts failed channel history reads (once per read, not per pending issue).

### Admin Controls

Actions that change an issue's state — resolving, muting, terminating — require admin rights, preventing accidental or unauthorised changes in busy shared channels. Admins can be defined globally (effective across every channel Slack Manager manages) or scoped to a specific channel, and membership can be specified by individual Slack user ID or by Slack user group. Non-admins can still react with `:eyes:` to signal they are investigating, keeping everyone able to participate without granting broad permissions.

## Architecture

```
External Systems → API Server → Alert Queue → Manager → Coordinator → Channel Manager → Database + Slack
                                                                   ↑
Slack Events ────────────────────────────────→ Socket Mode ────────┘
```

### API Server (`restapi`)

- REST server on port 8080 accepting alerts
- Endpoints: `POST /alert[s]`, `POST /prometheus-alert`, `GET /mappings`, `GET /channels`, `GET /ping`
  - `POST /slackalert[s]` are deprecated aliases for `POST /alert[s]`, kept for legacy clients
  - `GET /mappings` returns the routing rules in the legacy format, extended with `alertType`: `{"rules":[{"teamName", "description", "alertType", "equals", "hasPrefix", "matchesRegex", "channel", "matchAll"}]}`. An empty description falls back to the rule name. Legacy clients ignore `alertType`, so they may match typed rules that the API would not apply to their (untyped) alerts
- Channel names in `slackChannelId` and in escalation `moveToChannel` are mapped to channel IDs (for channels the Slack Manager is a member of)
- Per-channel rate limiting
- Validates and enqueues alerts for the manager to process

### Manager (`manager`)

- Dequeues and processes alerts from the alert queue
- Groups correlated alerts into "issues" per Slack channel
- Manages issue lifecycle: creation, grouping, escalation, resolution, archival
- Handles Slack interactions (reactions, slash commands, modals) via Socket Mode
- Supports distributed deployments via Redis-backed channel locking
- Optional legacy mode (`ManagerConfig.EnableLegacyFeatures`) for clients built against the legacy Slack Manager: honours the deprecated `deletionDelaySeconds` alert field, and adds the deprecated `input` key (a copy of `plainTextInput`) to webhook callbacks

### Deployment Topology

Both packages can run in a single binary (as shown in the Quick Start), which is fine for development and low-traffic environments. In production, they should be deployed as separate services.

The API server is stateless: it validates incoming alerts, applies rate limiting, and writes to the queue. It can be scaled horizontally behind a load balancer to absorb traffic spikes, and restarted or redeployed without consequence.

The manager is stateful and long-running. It holds open Slack Socket Mode connections, maintains per-channel state in memory, owns the issue lifecycle, and coordinates distributed locking. Restarting the manager interrupts active processing, and running multiple instances requires a Redis-backed locker to prevent race conditions. Keeping the manager isolated from the API means that a spike in inbound alert volume, a bad deployment of the API tier, or a rolling restart of the ingestion layer has no effect on the manager's stability.

In practice this means the API service can be treated like any other HTTP microservice — ephemeral, horizontally scaled, CI/CD-deployed frequently — while the manager is operated more like a stateful backend: given dedicated resources, updated carefully, and kept away from the blast radius of the ingestion layer.

## Insights

Insights is an optional, queryable history of what the Slack Manager does, intended for dashboards: alert outcomes, issue lifecycle, user commands, Slack posts and periodic channel snapshots. It complements logs and metrics: metrics remain the operational view, insights are the per-channel, per-issue, human-oriented view.

### Recording

The manager records insights events when an insights sink is available. The sink is discovered from the database (a `types.DB` that implements `types.InsightsSinkProvider`, e.g. the Postgres plugin with `postgres.WithInsightsEnabled()`), or set explicitly with `Manager.WithInsightsSink`. Without a sink, nothing is recorded and the cost is a nil check.

Recording never affects alert processing:

- Events are put in a bounded in-memory buffer with a non-blocking enqueue. When the buffer is full, events are dropped and counted (`insights_events_dropped_total{reason="buffer_full"}`).
- Events are built only from data already in memory. No extra database reads, Slack calls or locks.
- A single background goroutine writes events in batches, never as part of issue or alert writes.
- Sink errors and panics are recovered, counted and logged (rate-limited). After a failed write, the recorder backs off exponentially.
- At shutdown, buffered events are flushed with a hard deadline (`ShutdownFlushTimeoutMs`, default 2s).
- Lost events are reported in the event stream itself, as `insights.gap` events.

Tune or disable recording with `ManagerConfig.Insights` (`config.InsightsConfig`):

| Field | Default | Description |
|---|---|---|
| `Disabled` | `false` | Disable recording even if a sink is available |
| `BufferSize` | `10000` | In-memory buffer capacity |
| `BatchSize` | `500` | Maximum events per write |
| `FlushIntervalMs` | `1000` | Maximum wait before a partial batch is written |
| `WriteTimeoutMs` | `5000` | Deadline of a single write |
| `ShutdownFlushTimeoutMs` | `2000` | Maximum time spent flushing at shutdown |
| `ChannelStatsIntervalSeconds` | `300` | Interval between channel snapshots: at most one per channel with open issues per interval window, across all manager instances, plus a final zero snapshot when the channel becomes empty |

The API server can also record alert intake events (queued, filtered and rejected alerts) with `Server.WithInsightsSink` and `APIConfig.Insights`. This is optional; without it, rejections are still counted in the `alerts_rejected_total{reason}` and `alerts_filtered_total{reason}` metrics.

### Events

Every event has a type and an optional reason. Events carry identifiers and classifications only, never alert content. They do include some personal data: the alert host and author, and the Slack user ID and real name of users who run commands. These are kept for the storage retention (30 days by default in the Postgres plugin). All types, reasons and value keys are documented constants in `github.com/slackmgr/types` (`InsightsEvent*`, `InsightsReason*`, `InsightsValue*`). Key definitions:

| Concept | Definition |
|---|---|
| Accepted alert | Processed by the manager and acked as successful: `alert.new_issue` + `alert.grouped` + `alert.ignored` |
| Ignored alert | Processed and saved, but intentionally without effect. Reasons: `not_newer`, `already_resolved`, `follow_up_disabled`, `resolved_without_issue` |
| Dropped alert | Queued, but permanently discarded by the manager without processing: `invalid`, `missing_encryption_key`, `corrupt_issue`, `unreadable` |
| Retried alert | Processing attempt aborted and the alert returned to the queue (not terminal): `lock_timeout`, `gate_timeout`, `slack_error`, `db_error`, `move_race` |
| Rejected / filtered alert | Refused by the API (client got an error), or discarded by the alert's own `ignoreIfTextContains` rule (API recording only) |
| Updated issue | An existing open issue changed by a new alert (`issue.updated`) |
| Resolved / reopened issue | Explicit transition by an alert or a user. Auto-resolve by timeout is implicit, and shown in `issue.archived` and in the current state |
| Escalated issue | An escalation rule raised the severity or became active for the first time |
| Archived issue | Reason is how the issue ended (`user_resolved`, `alert_resolved`, `auto_resolved`, `auto_resolved_inconclusive`, `terminated`, `fire_and_forget`), with lifecycle values (open time, time to resolve, time to first investigation, alert and reopen counts) |

Delivery is best effort: events can be lost (shown by `insights.gap`) and, in rare retry paths, duplicated.

### Reading

The `insights` package answers dashboard questions on top of a `types.InsightsReader`:

```go
reader := postgres.NewInsightsReader(logger, opts...) // read-only, never runs migrations
_ = reader.Connect(ctx)
_ = reader.CheckSchema(ctx)

svc := insights.New(reader)
f := insights.Filter{From: from, To: to, ChannelIDs: channels}

summary, _ := svc.Summary(ctx, f)                       // all activity counts
series, _ := svc.Series(ctx, insights.SeriesQuery{Filter: f, Bucket: time.Hour,
    Metrics: []insights.Metric{{Type: types.InsightsEventIssueCreated}}})
state, _ := svc.CurrentState(ctx, insights.CurrentStateQuery{ChannelIDs: channels})
```

| Method | Answers |
|---|---|
| `Summary` | Counts of all alert, issue, command, Slack post and webhook events, by reason, plus lost events |
| `Series` | Zero-filled time series of any event type (and reason), optionally per channel |
| `OpenIssuesSeries` | Open issues over time per channel, from channel snapshots |
| `Breakdown` | Top-N values of a dimension, e.g. noisiest route keys, hosts or correlation IDs, most active users |
| `CurrentState` | Open issues by channel and severity, resolved-but-not-archived, muted, investigated, escalated, moved, stale; oldest, stale and noisiest issues |
| `Lifecycle` | Time to resolve, time to first investigation, open time, alerts and reopens per issue (avg, p50, p90, p99, max); resolution mix |
| `Flapping` | Issues reopened most often |
| `MoveFlows` | Issue moves between channels, by reason |
| `IssueTimeline` / `Events` | Paginated event listings, e.g. all events of one issue |

Activity methods return `types.ErrInsightsNotSupported` when no reader is available. A query matching more than `types.MaxInsightsRows` (10,000) groups fails with `types.ErrInsightsTooManyRows` rather than returning truncated results. This mainly affects `Series` with `ByChannel` and `OpenIssuesSeries` (one row per bucket and channel, and for `Series` also per event type, and per reason if a metric has one); use a shorter range, a larger bucket or fewer channels. `CurrentState` also works with any `types.DB` (`insights.NewFromDB` or `insights.WithDB`), including plugins without insights support.

## Metrics and Grafana Dashboard

Both `restapi` and `manager` emit metrics through the injected `types.Metrics` implementation, with all names prefixed by `MetricsPrefix` (default `slackmgr_`). A ready-made Grafana dashboard for a Prometheus data source is available in [`dashboards/slackmgr.json`](dashboards/slackmgr.json). It covers every metric emitted by the library: alert and issue flow, open issues, Slack API health, Slack posts (including pending posts), queue processing, lock contention and DB cache, the REST API, alert intake and the insights recorder. If you use a custom prefix, set the dashboard's "Metrics prefix" variable (default `slackmgr_`) to match, either in the dashboard settings or with the `var-prefix` URL parameter.

## Installation

```bash
go get github.com/slackmgr/core
```

Requires Go 1.26+.

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
        RestPort:      "8080",
        EncryptionKey: "your-32-char-key-here-0000000000", // optional; required for webhook support
        SlackClient:   &config.SlackClientConfig{BotToken: "xoxb-...", AppToken: "xapp-..."},
    }

    managerCfg := &config.ManagerConfig{
        EncryptionKey: "your-32-char-key-here-0000000000", // must match APIConfig.EncryptionKey
        SlackClient:   &config.SlackClientConfig{BotToken: "xoxb-...", AppToken: "xapp-..."},
    }

    server := restapi.New(alertProducer, logger, apiCfg).
        WithMetrics(metrics)
    
    mgr := manager.New(db, alertQueue, commandQueue, logger, managerCfg).
        WithLocker(locker).
        WithMetrics(metrics)

    g, ctx := errgroup.WithContext(ctx)
    
    g.Go(func() error { return server.Run(ctx) })
    g.Go(func() error { return mgr.Run(ctx) })

    if err := g.Wait(); err != nil {
        log.Fatal(err)
    }
}
```

## Plugins

The [`slackmgr/plugins`](https://github.com/slackmgr/plugins) repository provides ready-made implementations of the database and queue interfaces required by this library. Each plugin is a separate Go module versioned independently.

| Plugin | Module | Provides |
|--------|--------|----------|
| [postgres](https://github.com/slackmgr/plugins/tree/main/postgres) | `github.com/slackmgr/plugins/postgres` | PostgreSQL storage backend (`types.DB`) |
| [dynamodb](https://github.com/slackmgr/plugins/tree/main/dynamodb) | `github.com/slackmgr/plugins/dynamodb` | AWS DynamoDB storage backend (`types.DB`) |
| [sqs](https://github.com/slackmgr/plugins/tree/main/sqs) | `github.com/slackmgr/plugins/sqs` | AWS SQS queue consumer and webhook handler |
| [pubsub](https://github.com/slackmgr/plugins/tree/main/pubsub) | `github.com/slackmgr/plugins/pubsub` | Google Cloud Pub/Sub queue consumer and webhook handler |

For Redis-backed queues and channel locking (used in the Quick Start above), no additional plugin is needed — these are included in this library's `manager` package.

## Slack App Setup

A `manifest.json` is included in this repository. Use it to create or configure your Slack app at [api.slack.com/apps](https://api.slack.com/apps).

The app requires the following bot token scopes: `app_mentions:read`, `channels:history`, `channels:join`, `channels:manage`, `channels:read`, `chat:write`, `chat:write.customize`, `commands`, `emoji:read`, `groups:history`, `groups:read`, `groups:write`, `incoming-webhook`, `reactions:read`, `reactions:write`, `users:read`, `users:read.email`, `usergroups:read`.

Socket Mode must be enabled. Two tokens are required:

- **Bot token** (`xoxb-...`) — used for all Slack API calls
- **App-level token** (`xapp-...`) with the `connections:write` scope — used for Socket Mode

## License

This project is licensed under the MIT License — see the [LICENSE](LICENSE) file for details.

Copyright (c) 2026 Peter Aglen
