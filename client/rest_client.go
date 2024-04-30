package client

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-resty/resty/v2"
)

type restClient struct {
	client  *resty.Client
	options *options
	logger  Logger
}

func newRestClient(baseURL string, logger Logger, options *options) (*restClient, error) {
	if baseURL == "" {
		return nil, errors.New("base URL must be set")
	}

	if logger == nil {
		return nil, errors.New("logger cannot be nil")
	}

	if options == nil {
		return nil, errors.New("client options cannot be nil")
	}

	restyLogger := &restyLogger{logger: logger}

	client := resty.New().
		SetBaseURL(baseURL).
		SetRetryCount(options.retryCount).
		SetRetryWaitTime(options.retryWaitTime).
		SetRetryMaxWaitTime(options.retryMaxWaitTime).
		AddRetryCondition(customRetryPolicy).
		SetLogger(restyLogger)

	return &restClient{
		client:  client,
		options: options,
		logger:  logger,
	}, nil
}

func customRetryPolicy(r *resty.Response, err error) bool {
	// Retry on all connection errors
	if err != nil {
		return true
	}

	// Retry on 429 and 5xx errors
	return r.StatusCode() == 429 || r.StatusCode() >= 500
}

func (r *restClient) ping(ctx context.Context) error {
	_, err := r.get(ctx, "ping")

	return err
}

func (r *restClient) sendAlerts(ctx context.Context, body []byte) error {
	return r.post(ctx, "alerts", body)
}

func (r *restClient) get(ctx context.Context, path string) ([]byte, error) {
	request := r.client.R().
		SetContext(ctx).
		SetHeader("Accept", "application/json")

	response, err := request.Get(path)
	if err != nil {
		return nil, fmt.Errorf("GET %s failed with error: %w", response.Request.URL, err)
	}

	if !response.IsSuccess() {
		return nil, fmt.Errorf("GET %s failed with status code %d: %w", response.Request.URL, response.StatusCode(), getStatusCodeErrorMessage(response))
	}

	return response.Body(), nil
}

func (r *restClient) post(ctx context.Context, path string, body []byte) error {
	request := r.client.R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetBody(body)

	response, err := request.Post(path)
	if err != nil {
		return fmt.Errorf("POST %s failed with error: %w", response.Request.URL, err)
	}

	if !response.IsSuccess() {
		return fmt.Errorf("POST %s failed with status code %d: %w", response.Request.URL, response.StatusCode(), getStatusCodeErrorMessage(response))
	}

	return nil
}

func getStatusCodeErrorMessage(response *resty.Response) error {
	body := response.Body()

	if len(body) > 0 {
		return errors.New(string(body))
	}

	return errors.New("(empty error body)")
}
