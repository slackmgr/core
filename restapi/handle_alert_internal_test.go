package restapi

import (
	"testing"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReduceAlerts(t *testing.T) {
	t.Parallel()

	limit := 2

	alerts := make([]*common.Alert, 0, 3)
	alerts = append(alerts,
		&common.Alert{SlackChannelID: "123", Header: "a"},
		&common.Alert{SlackChannelID: "123", Header: "b"},
	)

	// Two alerts is within the limit -> no alerts should be skipped
	keptAlerts, skippedAlerts := reduceAlertCountForChannel("123", alerts, limit)
	assert.Len(t, keptAlerts, 2)
	assert.Empty(t, skippedAlerts)

	alerts = append(alerts, &common.Alert{
		SlackChannelID: "123", Header: "c",
	})

	// Three alerts is over the limit -> one alert should be skipped, and an overflow alert should be added
	keptAlerts, skippedAlerts = reduceAlertCountForChannel("123", alerts, limit)
	assert.Len(t, keptAlerts, 3)
	assert.Len(t, skippedAlerts, 1)
	assert.Equal(t, "a", keptAlerts[0].Header)
	assert.Equal(t, "b", keptAlerts[1].Header)
	assert.Equal(t, ":status: Too many alerts", keptAlerts[2].Header)

	alerts = []*common.Alert{
		{SlackChannelID: "123", Header: "a"},
		{SlackChannelID: "123", Header: "b"},
		{SlackChannelID: "123", Header: "c"},
		{SlackChannelID: "123", Header: "d"},
	}

	// Four alerts is over the limit -> two alerts should be skipped, and an overflow alert should be added
	keptAlerts, skippedAlerts = reduceAlertCountForChannel("123", alerts, limit)
	assert.Len(t, keptAlerts, 3)
	assert.Len(t, skippedAlerts, 2)
	assert.Equal(t, "a", keptAlerts[0].Header)
	assert.Equal(t, "b", keptAlerts[1].Header)
	assert.Equal(t, ":status: Too many alerts", keptAlerts[2].Header)
}

func TestAlertInputParser(t *testing.T) {
	t.Parallel()

	t.Run("array input", func(t *testing.T) {
		t.Parallel()
		input := `[{"header":"foo"}, {"header":"bar"}]`
		alerts, err := parseAlertInput([]byte(input))
		require.NoError(t, err)
		assert.Len(t, alerts, 2)
		assert.Equal(t, "foo", alerts[0].Header)
		assert.Equal(t, "bar", alerts[1].Header)
	})

	t.Run("array input with invalid json should return an error", func(t *testing.T) {
		t.Parallel()
		input := `[{"header":"foo"`
		_, err := parseAlertInput([]byte(input))
		require.Error(t, err)
	})

	t.Run("object input with all fields at root", func(t *testing.T) {
		t.Parallel()
		input := `{"header":"foo", "footer":"bar"}`
		alerts, err := parseAlertInput([]byte(input))
		require.NoError(t, err)
		assert.Len(t, alerts, 1)
		assert.Equal(t, "foo", alerts[0].Header)
		assert.Equal(t, "bar", alerts[0].Footer)
	})

	t.Run("object input with invalid json should return an error", func(t *testing.T) {
		t.Parallel()
		input := `{"header":"foo", `
		_, err := parseAlertInput([]byte(input))
		require.Error(t, err)
	})

	t.Run("object input with array of alerts", func(t *testing.T) {
		t.Parallel()
		input := `{"alerts":[{"header":"foo"}, {"header":"bar"}]}`
		alerts, err := parseAlertInput([]byte(input))
		require.NoError(t, err)
		assert.Len(t, alerts, 2)
		assert.Equal(t, "foo", alerts[0].Header)
		assert.Equal(t, "bar", alerts[1].Header)
	})
}

func TestCreateRateLimitAlert(t *testing.T) {
	t.Parallel()

	t.Run("creates alert with correct fields", func(t *testing.T) {
		t.Parallel()

		template := &common.Alert{
			IconEmoji: ":warning:",
			Username:  "TestBot",
		}

		alert := createRateLimitAlert("C123", 5, template)

		assert.Equal(t, "__rate_limit_C123", alert.CorrelationID)
		assert.Equal(t, ":status: Too many alerts", alert.Header)
		assert.Equal(t, "5 alerts were dropped", alert.Text)
		assert.Equal(t, "Too many alerts", alert.FallbackText)
		assert.Equal(t, ":warning:", alert.IconEmoji)
		assert.Equal(t, "TestBot", alert.Username)
		assert.Equal(t, "C123", alert.SlackChannelID)
	})

	t.Run("copies IssueFollowUpEnabled from template", func(t *testing.T) {
		t.Parallel()

		template := &common.Alert{
			IssueFollowUpEnabled: true,
		}

		alert := createRateLimitAlert("C123", 3, template)

		assert.True(t, alert.IssueFollowUpEnabled)
		assert.Equal(t, 3600, alert.AutoResolveSeconds)
	})

	t.Run("does not set AutoResolveSeconds when IssueFollowUpEnabled is false", func(t *testing.T) {
		t.Parallel()

		template := &common.Alert{
			IssueFollowUpEnabled: false,
		}

		alert := createRateLimitAlert("C123", 2, template)

		assert.False(t, alert.IssueFollowUpEnabled)
		assert.Equal(t, 0, alert.AutoResolveSeconds)
	})

	t.Run("handles single alert overflow", func(t *testing.T) {
		t.Parallel()

		template := &common.Alert{}
		alert := createRateLimitAlert("C456", 1, template)

		assert.Equal(t, "1 alerts were dropped", alert.Text)
	})
}
