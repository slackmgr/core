# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Duplicate Slack post prevention: when creating a post fails with an unknown outcome (timeout, HTTP 5xx, `fatal_error`, `internal_error`), the post is no longer retried. The issue is marked as pending (new `slackPostPendingSince` field in the stored issue JSON), and the post is looked up in the channel history before a new one is created. Found posts are adopted, and duplicates deleted. Issues with a pending post are not archived, and their post is not deleted (e.g. for escalation mentions) nor the issue moved, until it is resolved. Channel history reads are shared by all pending posts in a channel (cached for 10 seconds, failed reads retried after 30 seconds)
- Write-ahead save of issues before their Slack post is created, so that a post created right before a crash is adopted rather than posted again. Moved issues are now moved in the database (with a pending post) before the post in the target channel is created, so that the target channel does not create a post of its own if the target channel lock expires during the move
- Issue posts carry message metadata (event type `slackmgr_issue`, or `slackmgr_issue_deleted` for soft-deleted posts, with the issue ID as payload). Metadata is visible to all users and apps in the workspace
- `slack_pending_posts_total{result}` metric: Slack posts with unknown outcome, and how they were resolved (`pending`, `adopted`, `not_found`, `expired`, `lookup_failed`, `duplicate_deleted`)
- API rejects alerts to read-only channels with `400` and reason `channel_read_only` (`alerts_rejected_total`). New `ChannelInfo.ChannelIsReadOnly` field

### Fixed
- More permanent Slack errors are handled per issue instead of failing on every channel update cycle:
  - Channel unavailable (issue marked with an invalid-channel post ID): `is_inactive` (frozen or inactive channel), `invalid_channel_id`, `restricted_action`, `restricted_action_read_only_channel`, `restricted_action_thread_only_channel`, `ekm_access_denied`
  - Message can no longer be edited (a new post is created): `edit_window_closed`
  - Invalid message content (issue marked as invalid blocks): `invalid_blocks_format`, `msg_blocks_too_long`, `msg_too_long`, `too_many_attachments`, `attachment_payload_limit_exceeded`, `block_mismatch`, `no_text`
- Slack `update_failed` errors are retried as transient
- A failed Slack post create, update or delete for one issue no longer blocks the remaining issues in the channel update cycle. The cycle is only cut short for errors that affect all Slack calls: cancelled context, exhausted rate limit or transient error retries, and authentication errors (`not_authed`, `invalid_auth`, `account_inactive`, `token_revoked`, `token_expired`, `missing_scope`). All errors from the cycle are returned together
- Data race in the channel update cycle, where concurrent Slack post deletes changed issue state read by the re-ordering check

## [0.16.0] - 2026-10-21

### Added
- Insights: an optional, queryable history of what the Slack Manager does, for dashboards. Requires `github.com/slackmgr/types` v0.8.0
  - The manager records insights events (alert outcomes, issue lifecycle, commands, webhooks, Slack posts, and channel snapshots, one per channel and interval across all instances) when an insights sink is available, discovered from the database (`types.InsightsSinkProvider`) or set with `Manager.WithInsightsSink`. Recording is asynchronous and isolated: non-blocking enqueue into a bounded buffer, batched writes on a background goroutine, contained errors and panics, backoff, and a hard shutdown flush deadline
  - `ManagerConfig.Insights` and `APIConfig.Insights` (`config.InsightsConfig`) to tune or disable recording
  - `Server.WithInsightsSink`: optional recording of alert intake events (queued, filtered, rejected) in the API server
  - New `insights` package: `Recorder`, and `Service` with `Summary`, `Series`, `OpenIssuesSeries`, `Breakdown`, `CurrentState`, `Lifecycle`, `Flapping`, `MoveFlows`, `IssueTimeline` and `Events`. Queries that would exceed `types.MaxInsightsRows` groups (e.g. a long, fine-grained per-channel `Series`) fail with `types.ErrInsightsTooManyRows` instead of returning truncated series
  - Metrics `insights_events_written_total`, `insights_events_dropped_total{reason}`, `insights_write_duration_seconds` and `insights_buffer_size`
- `slack_posts_total{result}` metric: Slack post operations for issues (`created`, `updated`, `deleted`, `throttled`, `failed`)
- Issues track `firstInvestigatedAt` and `reopenCount` (new fields in the stored issue JSON)
- Grafana dashboard: alert intake, Slack posts and insights recorder panels
- `alerts_resolved_without_issue_total{channel}` metric: resolved alerts that the manager ignored because no open issue existed for the correlation ID (issue follow-up enabled). These alerts are still saved and acked as successful, but previously only produced a log line
- `alerts_rejected_total{reason}` metric (API): alerts rejected before queuing, by reason (`invalid_body`, `empty_request`, `validation`, `no_route`, `too_many_in_request`, `channel_lookup_failed`, `channel_not_found`, `channel_archived`, `not_in_channel`, `too_many_users`, `rate_limited`, `missing_encryption_key`, `encryption_failed`, `queue_error`). When a request fails, every alert in it that was not yet queued counts. Covers both HTTP endpoints and raw alert queue consumers
- `alerts_filtered_total{reason}` metric (API): valid alerts discarded because they matched an `ignoreIfTextContains` term

### Changed
- Bump `github.com/slackmgr/types` to v0.8.0
- Internal: the `models` package moved from `manager/internal/models` to `internal/models` (no public API change)

### Fixed
- An issue is now considered resolved from its resolve time (inclusive). Previously `IsResolved` was false until the clock advanced past the resolve time, so an issue could briefly appear unresolved right after an explicit resolve
- The manager now acks queue messages that cannot be unmarshalled, and counts them as `queue_messages_processed_total{result="unreadable"}`. Previously they were neither acked nor nacked. With Redis Streams this held the channel's stream lock until it expired, and because pending messages are reclaimed before new ones, a single unreadable message could stall all later alerts for that channel indefinitely. Note for SQS: unreadable messages are no longer redelivered and therefore no longer end up in a dead-letter queue; they are logged (with the error), counted and recorded as `alert.dropped/unreadable` insights events instead
- A failure to save the alert record after its issue has been saved no longer nacks the alert. The error is logged and the alert is acked. Previously the redelivered alert was ignored as not newer by the already updated issue

## [0.15.0] - 2026-10-21

### Added
- `ManagerConfig.EnableLegacyFeatures` (default `false`): compatibility mode for clients built against the legacy Slack Manager. When enabled, the manager uses the deprecated `Alert.DeletionDelaySeconds` as the archiving delay when `ArchivingDelaySeconds` is not set, and populates the deprecated `WebhookCallback.Input` (JSON `input`) with a copy of `PlainTextInput`
- `POST /slackalert[/:slackChannelId]` and `POST /slackalerts[/:slackChannelId]`: deprecated aliases for the alert endpoints, kept for legacy clients

### Changed
- Bump `github.com/slackmgr/types` to v0.7.1 and update all direct dependencies, including `github.com/slack-go/slack` v0.30.1, `github.com/bsm/redislock` v0.10.0, `github.com/redis/go-redis/v9` v9.23.0, `github.com/eko/gocache/lib/v4` v4.4.0 and `github.com/stretchr/testify` v1.12.1
- `RedisChannelLocker.Obtain` now returns transient Redis errors as-is instead of `ErrChannelLockUnavailable` (behaviour change in `redislock` v0.10.0), so Redis outages are logged as errors rather than as lock contention
- CI and code scanning workflows read the Go version (major.minor) from `go.mod` and install the latest patch release (`check-latest: true`), so all jobs, including both govulncheck runs, use the same Go version. Previously CI ran exactly Go 1.26.0, and govulncheck failed on standard library vulnerabilities fixed in later patches
- README: Go requirement corrected to 1.26+
- `GET /mappings` now returns the legacy format, extended with `alertType`: `{"rules":[{"teamName","description","alertType","equals","hasPrefix","matchesRegex","channel","matchAll"}]}` instead of a bare array of routing rules (**breaking** for consumers of the previous format). An empty description falls back to the rule name

### Fixed
- Route keys and channel IDs with leading/trailing whitespace are now trimmed before routing and channel name mapping. Previously such alerts failed to match their routing rule or channel name
- Channel names in escalation `moveToChannel` are now mapped to channel IDs by the API. Previously they were uppercased by `Alert.Clean()` and silently ignored by the manager

## [0.14.0] - 2026-10-20

### Changed
- Update GitHub Actions to latest versions (`actions/checkout` v7.0.1, `actions/setup-go` v7.0.0, `github/codeql-action` v4.39.0, `golangci/golangci-lint-action` v9.3.0)
- Minimum Go version is now 1.26 (required by `golang.org/x/net` v0.60.0)
- Bump `golang.org/x/net` to v0.60.0 and `github.com/quic-go/quic-go` to v0.59.1 (security fixes)
- Prometheus webhooks no longer map the `failOnRateLimitError` label/annotation to the deprecated, unused `Alert.FailOnRateLimitError` field

### Fixed
- Data race in `restapi.Server.UpdateSettings`: API settings are now swapped atomically, so hot-reloading is safe while requests are being routed

## [0.13.0] - 2026-06-19

### Changed
- `POST /alert` now returns `400 Bad Request` (instead of `204 No Content`) when the request body is an empty alert array (`[]`), matching the `/prometheus-alert` handling of an empty alert list

### Fixed
- Six-figure alert counts no longer render as a hex color in the Slack footer

## [0.12.7] - 2026-05-11

### Added
- Support for `Webhook.SkipConfirmationDialog`: when true, clicking the webhook button dispatches the webhook command immediately instead of opening a confirmation modal
- Debounce on rapid webhook button clicks: repeated clicks on the same webhook within 2 seconds are ignored to prevent accidental double-triggering

### Changed
- Bump `github.com/slackmgr/types` to v0.6.0 (adds `Webhook.SkipConfirmationDialog`)
- Default webhook confirmation header no longer includes the webhook URL

## [0.12.6] - 2026-05-08

### Changed
- Bump `github.com/slackmgr/types` to v0.5.2 (`Alert.ValidateWebhooks()` no longer rejects `WebhookPlainTextInput.InitialValue` when shorter than `MinLength`)

## [0.12.5] - 2026-05-07

### Fixed
- `debugGetAlertChannelOrRouteKey` no longer panics with a nil pointer dereference when `writeErrorResponse` is called with a nil alert (e.g. empty POST body to `/alert(s)`) and `ErrorReportChannelID` is configured

## [0.12.4] - 2026-05-07

### Changed
- Bump `github.com/slackmgr/types` to v0.5.1 (godoc fixes on `Webhook` fields; `Clean()` now defaults empty `ButtonStyle` to `WebhookButtonStyleDefault` and empty `AccessLevel` to `WebhookAccessLevelGlobalAdmins`)
- `getWebhookButtons`: stop mutating `hook.ButtonStyle`; map `WebhookButtonStyleDefault` and invalid values to the empty Slack-API style locally

## [0.12.3] - 2026-05-06

### Changed
- Rename `WebhookCommandParams.Input` to `PlainTextInput` (JSON tag `"input"` → `"plainTextInput"`) for consistency with `types.WebhookCallback`
- Bump `github.com/slackmgr/types` to v0.5.0
- Add `goconst` to golangci-lint configuration

## [0.12.2] - 2026-04-24

### Changed
- DB cache metric operation names extracted into constants (`metricOperationSaveIssue`, `metricOperationFindMoveMapping`, `metricOperationFindProcessingState`)

## [0.12.1] - 2026-04-23

### Changed
- Grafana dashboard queries now aggregate across instances with `sum by (label)` / `max by (channel)` to prevent duplicate series in multi-pod deployments
- Metric `channel` labels now use the human-readable Slack channel name instead of the channel ID; falls back to the channel ID if the name cannot be resolved. `ChannelInfo` in `restapi` gains a `Name` field populated from the Slack API

## [0.12.0] - 2026-04-23

### Added
- Six new Prometheus metrics: `slack_api_call_duration_seconds` histogram (p50/p95/p99 latency per Slack API method), `alerts_grouped_total` (alerts added to an existing open issue), `alerts_ignored_total` (alerts absorbed as duplicates), `commands_processed_total` (commands by action type), `socket_mode_events_total` (incoming Socket Mode events by type), `http_alerts_rate_limited_total` (alert requests rejected by the per-channel rate limiter)
- Grafana dashboard (`dashboards/slackmgr.json`) covering all metrics across seven sections: Overview, Alert & Issue Flow, Open Issues & State, Slack API Health, Queue Processing, Lock Contention & DB Cache, REST API

### Fixed
- `commands_processed_total` is now incremented in `processCmd` (after `execCmd` returns) rather than mid-way through `execCmd`, ensuring exactly one increment per dispatched command regardless of which internal error path was taken
- `issue_hash` DB cache hit ratio was always zero: `LoadOpenIssuesInChannel` was seeding the cache with raw DB JSON, which PostgreSQL JSONB normalises into a different key order than Go's marshaler produces, causing every hash comparison to fail; the cache is now seeded solely by `SaveIssues` using Go's canonical JSON

### Removed
- `slack_api_calls_total` counter removed; call rate is now derivable from the `slack_api_call_duration_seconds` histogram `_count` suffix

## [0.11.1] - 2026-04-22

### Fixed
- `http: Server closed` no longer logged as an error on graceful shutdown; `ErrServerClosed` is now treated as a clean exit from the HTTP server goroutine

## [0.11.0] - 2026-04-21

### Changed
- Slack API retry logic simplified: only explicitly transient errors (`internal_error`, `fatal_error`, `service_unavailable`, `request_timeout`, HTTP timeouts, and errors implementing `Retryable() bool`) are retried; all other errors fail immediately instead of going through a separate "fatal" retry path
- Slack error metric label (`slack_api_errors_total`) now uses the Slack error code string directly (e.g. `channel_not_found`, `internal_error`) instead of collapsing errors into broad categories, giving better observability
- Slack error constants renamed from `SlackFooError` to `SlackErrFoo` (e.g. `SlackChannelNotFoundError` → `SlackErrChannelNotFound`) for consistency with Go naming conventions

### Fixed
- Jitter (up to 500ms) added to all retry backoff delays (rate limit, transient) to prevent concurrent goroutines from retrying in lockstep

### Removed
- `MaxAttemptsForFatalError` and `MaxFatalErrorWaitTimeSeconds` fields removed from `config.SlackClientConfig`; the fatal retry path no longer exists

## [0.10.1] - 2026-04-14

### Changed

- Bump `github.com/slackmgr/types` dependency to v0.4.1

## [0.10.0] - 2026-04-08

### Changed
- Socket mode handler extracted into dedicated `socket_mode_handler.go` and `socket_mode_client.go`; `SocketModeHandlerFunc` signature simplified to `func(context.Context, *socketmode.Event)` with the `SocketModeClient` stored on each controller struct instead of passed per-call
- `SocketModeClient` interface gains `AckWithPayload` and `AckWithFieldErrorMsg` as first-class methods (previously package-level helpers)
- `views.IssueDetailsAssets` and internal `formatTimestamp` now use the package-level `config.Location()` instead of accepting a `*config.ManagerConfig` parameter
- `config.Location()` global is now race-safe via `atomic.Pointer`

### Removed
- Info channel concept removed

## [0.9.3] - 2026-04-07

### Fixed
- Slack message text blocks are now cut at natural break points (newlines, then spaces) instead of at arbitrary character positions, producing cleaner truncated messages

## [0.9.2] - 2026-03-30

### Fixed
- `duration` log field in `logAlerts` now passed as `time.Duration` instead of a pre-formatted string, giving structured logging backends a typed value

## [0.9.1] - 2026-03-23

### Changed
- `restapi.ServerHooks` renamed to `restapi.Hooks` for consistency with `manager.Hooks`
- `restapi.Hooks.OnStartup` doc comment updated to indicate it should also enable a liveness probe

## [0.9.0] - 2026-03-23

### Added
- `manager.Hooks` struct with `OnStartup` and `OnShutdown` callbacks; register via `Manager.WithHooks` for Kubernetes startup and liveness probe support
- `restapi.ServerHooks` struct with `OnStartup`, `OnReady`, `OnNotReady`, and `OnShutdown` callbacks; register via `Server.WithHooks` for Kubernetes startup, readiness, and liveness probe support
- `APIConfig.ShutdownTimeoutMs` — configurable HTTP server graceful shutdown timeout (default 3000 ms, range 500–30,000 ms); registered in `SetAPIConfigDefaults` for Viper users

### Changed
- HTTP server now uses `srv.Shutdown` instead of `srv.Close` on context cancellation, allowing in-flight requests to complete within the configured `ShutdownTimeoutMs` deadline
- `Server.Run` now uses `net.ListenConfig.Listen` instead of `net.Listen` so port binding respects context cancellation
- Raw alert consumer loop replaced with a `select` on both `queueCh` and `ctx.Done()`, ensuring the goroutine exits on context cancellation regardless of whether the consumer closes the channel

## [0.8.0] - 2026-03-23

### Added
- `DefaultsSetter` interface and `SetManagerConfigDefaults` / `SetAPIConfigDefaults` functions; call before `viper.Unmarshal` to register all non-zero config defaults so fields absent from the config file retain their defaults. The interface is satisfied by `*viper.Viper` via duck typing — no Viper import required in this module
- `config.DefaultLocation` constant (`"UTC"`)
- `GetLocation() *time.Location` method on `ManagerConfig`; result is cached after `Validate()` to avoid repeated filesystem I/O for timezone data
- `mapstructure` struct tags on all exported fields in `APIConfig`, `RateLimitConfig`, `ManagerConfig`, `SlackClientConfig`, `APISettings`, `RoutingRule`, `ManagerSettings`, and all nested settings types; required for correct `viper.Unmarshal` behaviour

### Changed
- **Breaking**: `ManagerConfig.Location` field type changed from `*time.Location` to `string` (IANA timezone name, e.g. `"UTC"`, `"America/New_York"`); use `GetLocation()` to obtain the parsed `*time.Location`
- **Breaking**: `CoordinatorDrainTimeout time.Duration`, `ChannelManagerDrainTimeout time.Duration`, and `SocketModeDrainTimeout time.Duration` replaced by `CoordinatorDrainTimeoutMs int`, `ChannelManagerDrainTimeoutMs int`, and `SocketModeDrainTimeoutMs int`; values are milliseconds, eliminating the YAML quoted-string requirement
- **Breaking**: Constants renamed: `MinDrainTimeout` → `MinDrainTimeoutMs`, `MaxDrainTimeout` → `MaxDrainTimeoutMs`, `DefaultCoordinatorDrainTimeout` → `DefaultCoordinatorDrainTimeoutMs`, `DefaultChannelManagerDrainTimeout` → `DefaultChannelManagerDrainTimeoutMs`, `DefaultSocketModeDrainTimeout` → `DefaultSocketModeDrainTimeoutMs`
- README rewritten with Features section and deployment guidance

## [0.7.1] - 2026-02-26

### Changed
- Runtime `Info` log statements now use static message strings with dynamic values in structured fields; `"event"` field renamed to `"event_type"` in socket mode internal event logs for consistency

## [0.7.0] - 2026-02-26

### Changed
- Requires `github.com/slackmgr/types` v0.4.0; all `types.Metrics` call sites updated to renamed methods (`CounterAdd`, `CounterInc`, `GaugeSet`, `GaugeAdd`)
- `db_cache_requests_total` and rate limit gate counters are now pre-warmed at startup so all label combinations appear at `/metrics` immediately
- `slack_client_requests_total` and `slack_client_cache_hits_total` replaced by `slack_client_cache_requests_total{slack_action, result="hit|miss"}`; hit ratio is now a direct PromQL ratio without subtraction
- `queue_messages_acked_total` and `queue_messages_nacked_total` replaced by `queue_messages_processed_total{queue_type, result}`; result values are `success`, `discarded`, `slack_error`, `db_error`, `move_race`, `lock_timeout`, `gate_timeout`
- `slack_api_rate_limit_duration_seconds` histogram `slack_action` label removed to reduce cardinality

### Fixed
- `active_channel_managers` gauge now correctly reflects the number of active channel managers (was a no-op due to using the wrong metrics method)
- `ResolveTime` and `ArchiveTime` are now calculated from `LastAlertReceived` instead of `alert.Timestamp`, preventing premature archiving for stale-timestamped alerts
- Log timestamps (`last_alert_timestamp`, `resolve_time`, `archive_time`) normalized to UTC, eliminating mixed-timezone output in structured logs

## [0.6.0] - 2026-02-25

### Added
- `config.InfoChannelSettings.TemplateContent` field as an alternative to `TemplatePath`; supports both plain and standard base64-encoded (RFC 4648) Go template strings with transparent auto-detection. Preferred over `TemplatePath` in environments where file mounts are inconvenient (e.g. Kubernetes — embed the template directly in the settings ConfigMap)
- Expanded metrics instrumentation across `manager` and `restapi` packages

### Changed
- Added unit tests for `views.GreetingView`, `views.WebhookInputView`, and `views.InfoChannelView`

## [0.5.1] - 2026-02-24

### Changed
- Bump dependencies to latest versions

## [0.5.0] - 2026-02-24

### Added
- `config.ManagerConfig.IsSingleInstanceDeployment` flag; when true, a nil `ChannelLocker` is accepted and multi-instance safeguards are skipped

### Changed
- **Breaking**: `manager.New()` reduced from 9 positional parameters to 5 required ones (`db`, `alertQueue`, `commandQueue`, `logger`, `cfg`); optional dependencies (`cacheStore`, `locker`, `metrics`, `managerSettings`) are now set via chainable `WithCacheStore`, `WithLocker`, `WithMetrics`, and `WithSettings` methods
- **Breaking**: `restapi.New()` reduced from 6 positional parameters to 3 required ones (`alertQueue`, `logger`, `cfg`); optional dependencies (`cacheStore`, `metrics`, `settings`) are now set via chainable `WithCacheStore`, `WithMetrics`, and `WithSettings` methods
- **Breaking**: `manager.New()` no longer accepts a `RateLimitGate` parameter; the gate is derived automatically from the locker via `WithLocker()` — `RedisChannelLocker` produces a `RedisRateLimitGate`, all other lockers fall back to `LocalRateLimitGate`
- All `With*` methods treat `nil` as a no-op, consistent with the existing `RegisterWebhookHandler` and `WithRawAlertConsumer` patterns
- Default go-cache store is now applied lazily in `Run()` rather than eagerly in `New()`, so callers that chain `WithCacheStore` do not allocate a discarded instance
- Reduce default throttle duration; throttle is now gated on active issue count rather than a fixed timer
- Reduce channel buffer sizes in coordinator and queue consumers

### Fixed
- Rate-limit throttle now counts only active (non-archived) issues, preventing over-throttling when most issues in a channel are already resolved

## [0.4.1] - 2026-02-24

### Changed
- `NewRedisRateLimitGate` now accepts variadic `RedisRateLimitGateOption` instead of positional `keyPrefix` and `maxDrainWait` parameters; use `WithRateLimitGateKeyPrefix` and `WithRateLimitGateMaxDrainWait` to override defaults
- `config.DefaultKeyPrefix` exported as the single source of truth for the `"slack-manager:"` Redis key namespace; `NewDefaultAPIConfig`, `NewDefaultManagerConfig`, and both Redis options constructors now reference it

## [0.4.0] - 2026-02-24

### Added
- `RateLimitGate` interface with `LocalRateLimitGate` (in-process) and `RedisRateLimitGate` (distributed) implementations; when any caller receives a Slack 429, all channel managers pause until the window expires and Socket Mode is quiet
- Global API concurrency semaphore in `SlackAPIClient` (size = `Concurrency` config); all Slack HTTP calls are now gated, not just batch deletions in `Update()`
- `manager.New()` accepts an optional `RateLimitGate` as its last parameter (`nil` uses `LocalRateLimitGate`)

### Changed
- **Breaking**: `SlackClient.Delete()` no longer takes a `*semaphore.Weighted` parameter; concurrency is now enforced globally inside `SlackAPIClient`

### Fixed
- Prevent duplicate issues when concurrent escalation and new-alert processing race to create the same issue in a channel

## [0.3.0] - 2026-02-23

### Added
- `Ratelimit-Limit`, `Ratelimit-Remaining`, and `Ratelimit-Reset` informational headers (IETF draft `ratelimit-headers`) on every `POST /alert` response; multi-channel requests report the most constrained channel
- Code scanning CI workflow uploading gosec and govulncheck results as SARIF to the GitHub Code Scanning dashboard
- `govulncheck` step added to `make test`

### Changed
- Rate limiting now rejects requests **immediately** with HTTP 429 and a `Retry-After` header instead of blocking the goroutine until tokens become available; this removes request latency caused by waiting and makes back-pressure explicit to callers
- **Breaking**: `MaxRequestWaitTime` field removed from `RateLimitConfig`; the corresponding constants (`MinMaxRequestWaitTime`, `MaxMaxRequestWaitTime`) and validation rule are also removed
- `InfDuration` guard added to `checkRateLimit`: when `count > burst`, `ReserveN` returns `rate.InfDuration`; the reservation is cancelled and the delay is capped at 24 h so callers never observe an overflow value
- CI: Security job now enforces gosec via `go install` (replaces Docker action); made the enforcement gate for the `Security` check
- CI: Add path filters to push and pull_request triggers
- README header updated to `slackmgr/core`
- Ignore `*.sarif` files in git

### Fixed
- gosec SSA panic in CI Security job resolved by switching to stable Go toolchain
- Go module cache re-enabled in the Security CI job

## [0.2.3] - 2026-02-20

### Changed
- Make `EncryptionKey` optional in `APIConfig` and `ManagerConfig`: an empty key is now valid and enables graceful degradation. The API rejects alerts with webhook payloads (HTTP 400) and the manager discards them with an error log, instead of blocking startup for deployments that do not use webhooks

### Fixed
- Fix ack/nack error handling in `processAlert` to prevent permanent failures from causing infinite retry loops: validation failures and corrupt DB records (`json.Unmarshal` failure, `nil` `LastAlert`) now ack the message rather than nacking it. Errors from `cleanAlertEscalations` are now propagated and nacked instead of being silently swallowed

## [0.2.2] - 2026-02-20

### Changed
- Enforce non-nil channel locker in `Manager.Run`; passing a nil locker now returns an explicit error instead of panicking later in a distributed environment

## [0.2.1] - 2026-02-19

### Added
- GitHub Actions CI workflow running `gosec`, `go vet`, `go test -race`, and `golangci-lint` on every push and pull request

### Changed
- Bump `go-resty/resty/v2` to v2.17.2
- Bump `redis/go-redis/v9` to v9.18.0
- Bump `bytedance/sonic` to v1.15.0 and `bytedance/sonic/loader` to v0.5.0; add `bytedance/gopkg` v0.1.3
- Bump `gabriel-vasile/mimetype` to v1.4.13
- Bump `go-playground/validator/v10` to v10.30.1
- Bump `goccy/go-yaml` to v1.19.2
- Bump `prometheus/common` to v0.67.5
- Bump `quic-go/qpack` to v0.6.0 and `quic-go/quic-go` to v0.59.0
- Bump `stretchr/objx` to v0.5.3
- Bump `ugorji/go/codec` to v1.3.1
- Bump `golang.org/x/arch` to v0.24.0, `golang.org/x/crypto` to v0.48.0, `golang.org/x/exp` to v0.0.0-20260218203240, `golang.org/x/net` to v0.50.0, `golang.org/x/sys` to v0.41.0, `golang.org/x/text` to v0.34.0
- Increase `golangci-lint` CI timeout from 1m to 5m

## [0.2.0] - 2026-02-19

### Changed
- Rename module to `github.com/slackmgr/core`: update module path from `github.com/peteraglen/slack-manager`, all import paths, and CHANGELOG comparison links
- Update common library dependency to `github.com/slackmgr/types` (renamed from `github.com/peteraglen/slack-manager-common`); replace stale `commonlib` import alias with the unaliased `types` package name throughout

## [0.1.8] - 2026-01-22

### Changed
- Improve logging clarity and reduce noise in queue consumer code

## [0.1.7] - 2026-01-22

### Added
- `RedisFifoQueueProducer`: lightweight write-only producer for use cases that only need to enqueue messages

### Changed
- Refactor `RedisFifoQueue` for cleaner context cancellation handling

### Fixed
- Race conditions and atomicity issues in `RedisFifoQueue`

## [0.1.6] - 2026-01-22

### Changed
- Remove unused `SetDefaults` and unexport `RateLimitConfig.Validate`

## [0.1.5] - 2026-01-22

### Added
- Bounded concurrency and graceful shutdown for Socket Mode event handlers
- `SocketModeClient` interface for improved testability of Socket Mode handling
- Comprehensive unit tests for the `controllers` package (57% coverage) and `models` package (95.6% coverage)
- Comprehensive unit tests for the `restapi` package and `handle_alert.go` rate limiting logic
- Validation for minimum drain timeout values in graceful shutdown configuration
- Comprehensive input validation for `APIConfig`, `ManagerConfig`, `ManagerSettings`, and `SlackClientConfig`
- Documentation comments throughout config packages and public API types

### Changed
- Graceful shutdown with configurable drain phase for in-flight message processing before exit
- `ChannelLock.Release` no longer accepts a `context.Context` parameter (**breaking**)
- `SlackClient` extracted as an interface in the `restapi` package for improved testability
- Flatten `internal/slackapi` subpackage into the `internal` package
- Return JSON error responses from `restapi` instead of plain text
- Simplify rate limiting and improve error handling in `restapi`
- Improve webhook retry policy

### Fixed
- Timeout middleware not applying to registered routes in `Server.Run`
- Panic in `ToJSON` replaced with proper error return
- Typos and wrong format verbs in the Slack package
- Various bugs in models and internal packages

## [0.1.4] - 2026-01-14

### Fixed
- `XReadGroup` blocking forever in Redis FIFO queue

## [0.1.3] - 2026-01-14

### Fixed
- Deadlock in Redis FIFO queue at shutdown

## [0.1.2] - 2026-01-14

### Added
- Redis Streams FIFO queue implementation
- Improved test coverage for Redis FIFO queue

### Changed
- Replace panics with proper error handling in manager package

## [0.1.1] - 2026-01-02

### Added
- CLAUDE.md project guidance file

### Fixed
- Bug fixes and additional test coverage for config package

## [0.1.0] - 2026-01-02

### Added
- Support multiple raw alert consumers via `WithRawAlertConsumer`

### Changed
- Replace Gorilla mux/handlers with Gin framework for HTTP routing
- Rename `api` package to `restapi`
- Improve panic recovery in REST API

## [0.0.62] - (Previous Release)

See git history for changes in v0.0.62 and earlier versions.

[Unreleased]: https://github.com/slackmgr/core/compare/v0.16.0...HEAD
[0.16.0]: https://github.com/slackmgr/core/compare/v0.15.0...v0.16.0
[0.15.0]: https://github.com/slackmgr/core/compare/v0.14.0...v0.15.0
[0.14.0]: https://github.com/slackmgr/core/compare/v0.13.0...v0.14.0
[0.13.0]: https://github.com/slackmgr/core/compare/v0.12.7...v0.13.0
[0.12.7]: https://github.com/slackmgr/core/compare/v0.12.6...v0.12.7
[0.12.6]: https://github.com/slackmgr/core/compare/v0.12.5...v0.12.6
[0.12.5]: https://github.com/slackmgr/core/compare/v0.12.4...v0.12.5
[0.12.4]: https://github.com/slackmgr/core/compare/v0.12.3...v0.12.4
[0.12.3]: https://github.com/slackmgr/core/compare/v0.12.2...v0.12.3
[0.12.2]: https://github.com/slackmgr/core/compare/v0.12.1...v0.12.2
[0.12.1]: https://github.com/slackmgr/core/compare/v0.12.0...v0.12.1
[0.12.0]: https://github.com/slackmgr/core/compare/v0.11.1...v0.12.0
[0.11.1]: https://github.com/slackmgr/core/compare/v0.11.0...v0.11.1
[0.11.0]: https://github.com/slackmgr/core/compare/v0.10.1...v0.11.0
[0.10.1]: https://github.com/slackmgr/core/compare/v0.10.0...v0.10.1
[0.10.0]: https://github.com/slackmgr/core/compare/v0.9.3...v0.10.0
[0.9.3]: https://github.com/slackmgr/core/compare/v0.9.2...v0.9.3
[0.9.2]: https://github.com/slackmgr/core/compare/v0.9.1...v0.9.2
[0.9.1]: https://github.com/slackmgr/core/compare/v0.9.0...v0.9.1
[0.9.0]: https://github.com/slackmgr/core/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/slackmgr/core/compare/v0.7.1...v0.8.0
[0.7.1]: https://github.com/slackmgr/core/compare/v0.7.0...v0.7.1
[0.7.0]: https://github.com/slackmgr/core/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/slackmgr/core/compare/v0.5.1...v0.6.0
[0.5.1]: https://github.com/slackmgr/core/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/slackmgr/core/compare/v0.4.1...v0.5.0
[0.4.1]: https://github.com/slackmgr/core/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/slackmgr/core/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/slackmgr/core/compare/v0.2.3...v0.3.0
[0.2.3]: https://github.com/slackmgr/core/compare/v0.2.2...v0.2.3
[0.2.2]: https://github.com/slackmgr/core/compare/v0.2.1...v0.2.2
[0.2.1]: https://github.com/slackmgr/core/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/slackmgr/core/compare/v0.1.8...v0.2.0
[0.1.8]: https://github.com/slackmgr/core/compare/v0.1.7...v0.1.8
[0.1.7]: https://github.com/slackmgr/core/compare/v0.1.6...v0.1.7
[0.1.6]: https://github.com/slackmgr/core/compare/v0.1.5...v0.1.6
[0.1.5]: https://github.com/slackmgr/core/compare/v0.1.4...v0.1.5
[0.1.4]: https://github.com/slackmgr/core/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/slackmgr/core/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/slackmgr/core/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/slackmgr/core/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/slackmgr/core/compare/v0.0.62...v0.1.0
[0.0.62]: https://github.com/slackmgr/core/releases/tag/v0.0.62
