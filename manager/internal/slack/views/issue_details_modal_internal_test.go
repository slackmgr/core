package views

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/slackmgr/core/config"
	"github.com/slackmgr/core/manager/internal/models"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- formatDuration ----

func TestFormatDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		d        time.Duration
		expected string
	}{
		{"72 hours → 3 days", 72 * time.Hour, "3 days"},
		{"just over 48 hours → 2 days", 49 * time.Hour, "2 days"},
		{"2 hours", 2 * time.Hour, "2 hours"},
		// 90 minutes is > 1h so it hits the "> time.Hour" branch: int(1.5) = 1 → "1 hours"
		{"90 minutes → 1 hours (not exact-hour branch)", 90 * time.Minute, "1 hours"},
		{"exactly 1 hour", time.Hour, "1 hour"},
		{"30 minutes", 30 * time.Minute, "30 minutes"},
		{"exactly 1 minute", time.Minute, "1 minute"},
		{"45 seconds", 45 * time.Second, "45 seconds"},
		{"zero → 0 seconds", 0, "0 seconds"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, formatDuration(tt.d))
		})
	}
}

// ---- formatTimestamp ----

func TestFormatTimestamp(t *testing.T) {
	t.Parallel()

	cfg := &config.ManagerConfig{Location: "UTC"}

	tests := []struct {
		name   string
		offset time.Duration // relative to time.Now()
		want   string        // expected substring in the result
	}{
		{"72 hours ago → days", -72 * time.Hour, "days ago"},
		{"2 hours ago → hours", -2 * time.Hour, "hours ago"},
		{"5 minutes ago → minutes", -5 * time.Minute, "minutes ago"},
		{"30 seconds ago → seconds", -30 * time.Second, "seconds ago"},
		{"just now (0s offset)", 0, "just now"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := time.Now().Add(tt.offset)
			result := formatTimestamp(ts, cfg)
			assert.Contains(t, result, tt.want,
				"formatTimestamp(%v): expected %q in %q", tt.offset, tt.want, result)
		})
	}
}

func TestFormatTimestamp_FormatsInConfiguredTimezone(t *testing.T) {
	t.Parallel()

	// A fixed time in UTC: 2024-06-15 12:00:00
	ts := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

	utcCfg := &config.ManagerConfig{Location: "UTC"}
	result := formatTimestamp(ts, utcCfg)
	assert.True(t, strings.HasPrefix(result, "2024-06-15T12:00:00"),
		"UTC result should start with the UTC-formatted timestamp, got %q", result)

	// New York is UTC-4 or UTC-5, so the same instant shows as 08:00:00 or 07:00:00.
	nyCfg := &config.ManagerConfig{Location: "America/New_York"}
	nyResult := formatTimestamp(ts, nyCfg)
	assert.False(t, strings.HasPrefix(nyResult, "2024-06-15T12:00:00"),
		"NY-timezone result should NOT start with the UTC timestamp, got %q", nyResult)
}

// ---- IssueDetailsAssets ----

// newMinimalIssue returns the smallest valid *models.Issue that IssueDetailsAssets
// can render without error.
func newMinimalIssue() *models.Issue {
	now := time.Now()
	return &models.Issue{
		ID:                "issue-id",
		CorrelationID:     "corr-id",
		LastAlert:         &models.Alert{Alert: types.Alert{SlackChannelID: "C123", Severity: types.AlertError}},
		Created:           now,
		LastAlertReceived: now,
		AlertCount:        1,
		ArchiveTime:       now.Add(time.Hour),
	}
}

func TestIssueDetailsAssets_NoError(t *testing.T) {
	t.Parallel()

	blocks, err := IssueDetailsAssets(newMinimalIssue(), config.NewDefaultManagerConfig())

	require.NoError(t, err)
	assert.NotEmpty(t, blocks.BlockSet)
}

func TestIssueDetailsAssets_MoveReason(t *testing.T) {
	t.Parallel()

	cfg := config.NewDefaultManagerConfig()

	tests := []struct {
		name        string
		isMoved     bool
		moveReason  models.MoveIssueReason
		movedByUser string
		wantJSON    string
	}{
		{
			name:     "not moved → dash",
			isMoved:  false,
			wantJSON: "*Reason*: `-`",
		},
		{
			name:       "escalation rule",
			isMoved:    true,
			moveReason: models.MoveIssueReasonEscalation,
			wantJSON:   "Escalation",
		},
		{
			name:        "user command includes username",
			isMoved:     true,
			moveReason:  models.MoveIssueReasonUserCommand,
			movedByUser: "alice",
			wantJSON:    "User command (alice)",
		},
		{
			name:       "unknown reason → raw value",
			isMoved:    true,
			moveReason: models.MoveIssueReason("CUSTOM"),
			wantJSON:   "CUSTOM",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			issue := newMinimalIssue()
			issue.IsMoved = tt.isMoved
			issue.MoveReason = tt.moveReason
			issue.MovedByUser = tt.movedByUser

			blocks, err := IssueDetailsAssets(issue, cfg)
			require.NoError(t, err)

			data, marshalErr := json.Marshal(blocks)
			require.NoError(t, marshalErr)
			assert.Contains(t, string(data), tt.wantJSON)
		})
	}
}

func TestIssueDetailsAssets_RouteKey(t *testing.T) {
	t.Parallel()

	cfg := config.NewDefaultManagerConfig()

	t.Run("empty route key rendered as dash", func(t *testing.T) {
		t.Parallel()
		issue := newMinimalIssue()
		issue.LastAlert.RouteKey = ""

		blocks, err := IssueDetailsAssets(issue, cfg)
		require.NoError(t, err)

		data, marshalErr := json.Marshal(blocks)
		require.NoError(t, marshalErr)
		assert.Contains(t, string(data), "*Route key*: `-`")
	})

	t.Run("non-empty route key preserved", func(t *testing.T) {
		t.Parallel()
		issue := newMinimalIssue()
		issue.LastAlert.RouteKey = "team-alpha"

		blocks, err := IssueDetailsAssets(issue, cfg)
		require.NoError(t, err)

		data, marshalErr := json.Marshal(blocks)
		require.NoError(t, marshalErr)
		assert.Contains(t, string(data), "team-alpha")
	})
}

func TestIssueDetailsAssets_AutoResolve(t *testing.T) {
	t.Parallel()

	cfg := config.NewDefaultManagerConfig()

	t.Run("follow-up disabled → auto resolve is false", func(t *testing.T) {
		t.Parallel()
		issue := newMinimalIssue()
		issue.LastAlert.IssueFollowUpEnabled = false

		blocks, err := IssueDetailsAssets(issue, cfg)
		require.NoError(t, err)

		data, marshalErr := json.Marshal(blocks)
		require.NoError(t, marshalErr)
		assert.Contains(t, string(data), "*Auto resolve*: `false`")
	})

	t.Run("follow-up enabled → auto resolve includes duration", func(t *testing.T) {
		t.Parallel()
		issue := newMinimalIssue()
		issue.LastAlert.IssueFollowUpEnabled = true
		issue.AutoResolvePeriod = 2 * time.Hour

		blocks, err := IssueDetailsAssets(issue, cfg)
		require.NoError(t, err)

		data, marshalErr := json.Marshal(blocks)
		require.NoError(t, marshalErr)
		assert.Contains(t, string(data), "2 hours after last alert")
	})
}
