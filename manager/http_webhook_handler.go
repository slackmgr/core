package manager

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
)

type HTTPWebhookHandler struct {
	client *resty.Client
}

func NewHttpWebhookHandler(logger common.Logger, cfg *config.ManagerConfig) *HTTPWebhookHandler {
	restyLogger := newRestyLogger(logger)

	client := resty.New().
		SetRetryCount(2).
		SetRetryWaitTime(time.Second).
		AddRetryCondition(webhookRetryPolicy).
		SetLogger(restyLogger).
		SetTimeout(time.Duration(cfg.WebhookTimeoutSeconds) * time.Second)

	return &HTTPWebhookHandler{
		client: client,
	}
}

func (h *HTTPWebhookHandler) ShouldHandleWebhook(_ context.Context, target string) bool {
	return strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://")
}

func (h *HTTPWebhookHandler) HandleWebhook(ctx context.Context, target string, data *common.WebhookCallback, logger common.Logger) error {
	response, err := h.client.R().SetContext(ctx).SetBody(data).Post(target)
	if err != nil {
		return fmt.Errorf("webhook POST %s failed: %w", response.Request.URL, err)
	}

	logger.Debugf("Webhook POST %s %s", response.Request.URL, response.Status())

	if !response.IsSuccess() {
		return fmt.Errorf("webhook POST %s failed with status code %d", response.Request.URL, response.StatusCode())
	}

	return nil
}

func webhookRetryPolicy(r *resty.Response, err error) bool {
	return err == nil && r.StatusCode() >= 500
}
