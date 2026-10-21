// Package insights records and queries a structured, human-oriented history of what the Slack Manager does:
// alert outcomes, issue lifecycle, user commands, Slack posts and periodic channel snapshots.
//
// Insights complement logs and metrics. Metrics remain the operational view; insights are the queryable
// per-channel and per-issue view, intended for dashboards.
//
// # Recording
//
// [Recorder] writes [types.InsightsEvent] values asynchronously to a [types.InsightsSink], typically provided by
// the database plugin. Recording is designed to never affect alert processing: [Recorder.Record] never blocks,
// writes happen in batches on a background goroutine, and sink errors and panics are contained. The manager and
// the API server create and manage their recorder internally; most hosts never use [Recorder] directly.
//
// # Reading
//
// [Service] answers dashboard questions (summaries, time series, breakdowns, current state, lifecycle statistics,
// issue timelines) on top of a [types.InsightsReader].
package insights
