package api

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testLogger implements common.Logger for testing
type testLogger struct{}

func (l *testLogger) Debug(msg string)                                       {}
func (l *testLogger) Debugf(format string, args ...interface{})              {}
func (l *testLogger) Info(msg string)                                        {}
func (l *testLogger) Infof(format string, args ...interface{})               {}
func (l *testLogger) Warn(msg string)                                        {}
func (l *testLogger) Warnf(format string, args ...interface{})               {}
func (l *testLogger) Error(msg string)                                       {}
func (l *testLogger) Errorf(format string, args ...interface{})              {}
func (l *testLogger) WithField(key string, value interface{}) common.Logger  { return l }
func (l *testLogger) WithFields(fields map[string]interface{}) common.Logger { return l }
func (l *testLogger) HttpLoggingHandler() io.Writer                          { return nil }

// testQueue implements FifoQueueProducer for testing
type testQueue struct{}

func (q *testQueue) Send(ctx context.Context, slackChannelID, dedupID, body string) error {
	return nil
}

// testConsumer implements FifoQueueConsumer for testing
type testConsumer struct{}

func (c *testConsumer) Receive(ctx context.Context, sinkCh chan<- *common.FifoQueueItem) error {
	return nil
}

func TestMax(t *testing.T) {
	t.Parallel()

	t.Run("empty returns zero", func(t *testing.T) {
		t.Parallel()
		result := max()
		assert.Equal(t, 0, result)
	})

	t.Run("single value", func(t *testing.T) {
		t.Parallel()
		result := max(42)
		assert.Equal(t, 42, result)
	})

	t.Run("multiple values returns maximum", func(t *testing.T) {
		t.Parallel()
		result := max(1, 5, 3, 9, 2)
		assert.Equal(t, 9, result)
	})

	t.Run("all zeros returns zero", func(t *testing.T) {
		t.Parallel()
		result := max(0, 0, 0)
		assert.Equal(t, 0, result)
	})

	t.Run("negative values still finds max", func(t *testing.T) {
		t.Parallel()
		// Note: the implementation only returns max > 0
		result := max(-1, -5, -3)
		assert.Equal(t, 0, result) // All negatives, so max stays 0
	})

	t.Run("mix of positive and negative", func(t *testing.T) {
		t.Parallel()
		result := max(-1, 5, -3, 2)
		assert.Equal(t, 5, result)
	})
}

func TestGetRequestTimeout(t *testing.T) {
	t.Parallel()

	t.Run("uses max of rate limit config", func(t *testing.T) {
		t.Parallel()
		cfg := config.NewDefaultAPIConfig()
		cfg.RateLimit.MaxWaitPerAttemptSeconds = 10
		cfg.RateLimit.MaxAttempts = 3
		cfg.SlackClient.MaxRateLimitErrorWaitTimeSeconds = 5
		cfg.SlackClient.MaxTransientErrorWaitTimeSeconds = 5
		cfg.SlackClient.MaxFatalErrorWaitTimeSeconds = 5

		server := &Server{cfg: cfg}
		timeout := server.getRequestTimeout()

		// MaxWaitPerAttemptSeconds * MaxAttempts = 30, which is max
		assert.Equal(t, 31*time.Second, timeout)
	})

	t.Run("uses max of slack client config", func(t *testing.T) {
		t.Parallel()
		cfg := config.NewDefaultAPIConfig()
		cfg.RateLimit.MaxWaitPerAttemptSeconds = 1
		cfg.RateLimit.MaxAttempts = 1
		cfg.SlackClient.MaxRateLimitErrorWaitTimeSeconds = 120
		cfg.SlackClient.MaxTransientErrorWaitTimeSeconds = 30
		cfg.SlackClient.MaxFatalErrorWaitTimeSeconds = 30

		server := &Server{cfg: cfg}
		timeout := server.getRequestTimeout()

		// MaxRateLimitErrorWaitTimeSeconds = 120 is max
		assert.Equal(t, 121*time.Second, timeout)
	})
}

func TestUpdateSettings(t *testing.T) {
	t.Parallel()

	t.Run("nil settings creates empty settings", func(t *testing.T) {
		t.Parallel()
		server := &Server{
			logger:      &testLogger{},
			apiSettings: &config.APISettings{},
		}

		err := server.UpdateSettings(nil)
		require.NoError(t, err)
		assert.NotNil(t, server.apiSettings)
	})

	t.Run("valid settings are applied", func(t *testing.T) {
		t.Parallel()
		server := &Server{
			logger:      &testLogger{},
			apiSettings: &config.APISettings{},
		}

		newSettings := &config.APISettings{
			RoutingRules: []*config.RoutingRule{
				{
					Name:     "test-rule",
					Channel:  "C123456789",
					MatchAll: true,
				},
			},
		}

		err := server.UpdateSettings(newSettings)
		require.NoError(t, err)
		assert.Len(t, server.apiSettings.RoutingRules, 1)
	})

	t.Run("invalid settings return error", func(t *testing.T) {
		t.Parallel()
		server := &Server{
			logger:      &testLogger{},
			apiSettings: &config.APISettings{},
		}

		// Invalid: rule with no channel
		invalidSettings := &config.APISettings{
			RoutingRules: []*config.RoutingRule{
				{
					Name:     "test-rule",
					Channel:  "", // Invalid: empty channel
					MatchAll: true,
				},
			},
		}

		err := server.UpdateSettings(invalidSettings)
		require.Error(t, err)
	})
}

func TestWithRawAlertConsumer(t *testing.T) {
	t.Parallel()

	t.Run("sets raw alert consumer", func(t *testing.T) {
		t.Parallel()
		server := &Server{}
		consumer := &testConsumer{}

		result := server.WithRawAlertConsumer(consumer)

		assert.Same(t, server, result) // Returns same server for chaining
		assert.NotNil(t, server.rawAlertConsumer)
	})
}

func TestCreateClientErrorAlert(t *testing.T) {
	t.Parallel()

	t.Run("4xx error creates warning alert", func(t *testing.T) {
		t.Parallel()
		cfg := config.NewDefaultAPIConfig()
		cfg.ErrorReportChannelID = "C123456789"
		server := &Server{cfg: cfg}

		alert := server.createClientErrorAlert(
			errors.New("bad request"),
			400,
			nil,
			"C987654321",
		)

		assert.Equal(t, common.AlertWarning, alert.Severity)
		assert.Contains(t, alert.Header, "400")
		assert.Equal(t, "C123456789", alert.SlackChannelID)
	})

	t.Run("5xx error creates error alert", func(t *testing.T) {
		t.Parallel()
		cfg := config.NewDefaultAPIConfig()
		cfg.ErrorReportChannelID = "C123456789"
		server := &Server{cfg: cfg}

		alert := server.createClientErrorAlert(
			errors.New("internal error"),
			500,
			nil,
			"C987654321",
		)

		assert.Equal(t, common.AlertError, alert.Severity)
		assert.Contains(t, alert.Header, "500")
	})

	t.Run("empty target channel uses N/A", func(t *testing.T) {
		t.Parallel()
		cfg := config.NewDefaultAPIConfig()
		cfg.ErrorReportChannelID = "C123456789"
		server := &Server{cfg: cfg}

		alert := server.createClientErrorAlert(
			errors.New("test error"),
			400,
			nil,
			"",
		)

		assert.Contains(t, alert.Text, "N/A")
	})

	t.Run("debug text is included and sanitized", func(t *testing.T) {
		t.Parallel()
		cfg := config.NewDefaultAPIConfig()
		cfg.ErrorReportChannelID = "C123456789"
		server := &Server{cfg: cfg}

		debugText := map[string]string{
			"Header": "Test `header` with *special* chars",
			"Body":   "Some ~strikethrough~ and _italic_",
		}

		alert := server.createClientErrorAlert(
			errors.New("test error"),
			400,
			debugText,
			"C987654321",
		)

		// Special chars should be removed
		assert.NotContains(t, alert.Text, "`header`")
		assert.NotContains(t, alert.Text, "*special*")
		assert.Contains(t, alert.Text, "Header")
		assert.Contains(t, alert.Text, "Body")
	})

	t.Run("long debug values are truncated", func(t *testing.T) {
		t.Parallel()
		cfg := config.NewDefaultAPIConfig()
		cfg.ErrorReportChannelID = "C123456789"
		server := &Server{cfg: cfg}

		longValue := ""
		for i := 0; i < 150; i++ {
			longValue += "x"
		}

		debugText := map[string]string{
			"LongField": longValue,
		}

		alert := server.createClientErrorAlert(
			errors.New("test error"),
			400,
			debugText,
			"C987654321",
		)

		// Should be truncated to 100 chars (97 + "...")
		assert.Contains(t, alert.Text, "...")
	})
}
