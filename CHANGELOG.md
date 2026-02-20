# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/slackmgr/core/compare/v0.2.2...HEAD
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
