package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// AlertClient represents a client which accepts and forwards alerts
type AlertClient interface {
	Connect(ctx context.Context, baseURL string, logger Logger, clientOptions ...Options) (AlertClient, error)
	SendAlert(ctx context.Context, alert *Alert) error
	SendAlerts(ctx context.Context, alerts []*Alert) error
}

type alertClient struct {
	restClient *restClient
}

// NewAlertClient creates a new Slack alert client
func NewAlertClient() AlertClient {
	return &alertClient{}
}

func (c *alertClient) Connect(ctx context.Context, baseURL string, logger Logger, clientOptions ...Options) (AlertClient, error) {
	options := newClientOptions()

	for _, o := range clientOptions {
		o(options)
	}

	client, err := newRestClient(baseURL, logger, options)
	if err != nil {
		return nil, fmt.Errorf("failed to create rest client with error: %w", err)
	}

	if err = client.ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping alerts API with error: %w", err)
	}

	c.restClient = client

	return c, nil
}

func (c *alertClient) SendAlert(ctx context.Context, alert *Alert) error {
	if c == nil {
		return errors.New("alert client is nil")
	}

	if alert == nil {
		return errors.New("alert cannot be nil")
	}

	return c.SendAlerts(ctx, []*Alert{alert})
}

func (c *alertClient) SendAlerts(ctx context.Context, alerts []*Alert) error {
	if c == nil {
		return errors.New("alert client is nil")
	}

	if len(alerts) == 0 {
		return errors.New("alerts list cannot be empty")
	}

	alertsInput := &Alerts{
		Alerts: alerts,
	}

	body, err := json.Marshal(alertsInput)
	if err != nil {
		return fmt.Errorf("failed to marshal alerts list with error: %w", err)
	}

	return c.restClient.sendAlerts(ctx, body)
}

// func (c *alertClient) GetMappings(ctx context.Context) (*AlertMappings, error) {
// 	if c == nil {
// 		return nil, errors.New("alert client is nil")
// 	}

// 	data, err := c.restClient.get(ctx, "mappings")
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to get mappings: %w", err)
// 	}

// 	mappings := AlertMappings{}

// 	if err := json.Unmarshal(data, &mappings); err != nil {
// 		return nil, fmt.Errorf("failed to json unmarshal alert mappings: %w", err)
// 	}

// 	if err := mappings.Init(); err != nil {
// 		return nil, fmt.Errorf("failed to initialize and validate alert mappings: %w", err)
// 	}

// 	return &mappings, nil
// }
