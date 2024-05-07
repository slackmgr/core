package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/peteraglen/slack-manager/lib/common"
)

type AlertClient struct {
	restClient *restClient
}

func New() *AlertClient {
	return &AlertClient{}
}

func (c *AlertClient) Connect(ctx context.Context, baseURL string, logger common.Logger, clientOptions ...Option) (*AlertClient, error) {
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

func (c *AlertClient) Send(ctx context.Context, alerts []*Alert) error {
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
