package api

import (
	"testing"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/stretchr/testify/assert"
)

func TestReduceAlerts(t *testing.T) {
	limit := 2

	alerts := []*common.Alert{
		{SlackChannelID: "123", Header: "a"},
		{SlackChannelID: "123", Header: "b"},
	}

	// Two alerts is within the limit -> no alerts should be skipped
	keptAlerts, skippedAlerts := reduceAlertCountForChannel("123", alerts, limit)
	assert.Len(t, keptAlerts, 2)
	assert.Len(t, skippedAlerts, 0)

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
	t.Run("array input", func(t *testing.T) {
		input := `[{"header":"foo"}, {"header":"bar"}]`
		alerts, err := parseAlertInput([]byte(input))
		assert.NoError(t, err)
		assert.Len(t, alerts, 2)
		assert.Equal(t, "foo", alerts[0].Header)
		assert.Equal(t, "bar", alerts[1].Header)
	})

	t.Run("array input with invalid json should return an error", func(t *testing.T) {
		input := `[{"header":"foo"`
		_, err := parseAlertInput([]byte(input))
		assert.Error(t, err)
	})

	t.Run("object input with all fields at root", func(t *testing.T) {
		input := `{"header":"foo", "footer":"bar"}`
		alerts, err := parseAlertInput([]byte(input))
		assert.NoError(t, err)
		assert.Len(t, alerts, 1)
		assert.Equal(t, "foo", alerts[0].Header)
		assert.Equal(t, "bar", alerts[0].Footer)
	})

	t.Run("object input with invalid json should return an error", func(t *testing.T) {
		input := `{"header":"foo", `
		_, err := parseAlertInput([]byte(input))
		assert.Error(t, err)
	})

	t.Run("object input with array of alerts", func(t *testing.T) {
		input := `{"alerts":[{"header":"foo"}, {"header":"bar"}]}`
		alerts, err := parseAlertInput([]byte(input))
		assert.NoError(t, err)
		assert.Len(t, alerts, 2)
		assert.Equal(t, "foo", alerts[0].Header)
		assert.Equal(t, "bar", alerts[1].Header)
	})
}
