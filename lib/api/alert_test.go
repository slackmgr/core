package api

import (
	"testing"

	"github.com/peteraglen/slack-manager/lib/client"
	"github.com/stretchr/testify/assert"
)

func TestReduceAlerts(t *testing.T) {
	limit := 2

	alerts := []*client.Alert{
		{SlackChannelID: "123", Header: "a"},
		{SlackChannelID: "123", Header: "b"},
	}

	keptAlerts, skippedAlerts := reduceAlertCountForChannel("123", alerts, limit)
	assert.Len(t, keptAlerts, 2)
	assert.Len(t, skippedAlerts, 0)

	alerts = append(alerts, &client.Alert{
		SlackChannelID: "123", Header: "c",
	})

	keptAlerts, skippedAlerts = reduceAlertCountForChannel("123", alerts, limit)
	assert.Len(t, keptAlerts, 3)
	assert.Len(t, skippedAlerts, 1)
	assert.Equal(t, "a", keptAlerts[0].Header)
	assert.Equal(t, "b", keptAlerts[1].Header)
	assert.Equal(t, ":status: Too many alerts", keptAlerts[2].Header)

	alerts = []*client.Alert{
		{SlackChannelID: "123", Header: "a"},
		{SlackChannelID: "123", Header: "b"},
		{SlackChannelID: "123", Header: "c"},
		{SlackChannelID: "123", Header: "d"},
	}

	keptAlerts, skippedAlerts = reduceAlertCountForChannel("123", alerts, limit)

	assert.Len(t, keptAlerts, 3)
	assert.Len(t, skippedAlerts, 2)
	assert.Equal(t, "a", keptAlerts[0].Header)
	assert.Equal(t, "b", keptAlerts[1].Header)
	assert.Equal(t, ":status: Too many alerts", keptAlerts[2].Header)
}
