package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	cachestore "github.com/eko/gocache/lib/v4/store"
	gocache_store "github.com/eko/gocache/store/go_cache/v4"
	"github.com/gin-gonic/gin"
	gocache "github.com/patrickmn/go-cache"
	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/peteraglen/slack-manager/internal/slackapi"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
)

type FifoQueueConsumer interface {
	Receive(ctx context.Context, sinkCh chan<- *common.FifoQueueItem) error
}

type FifoQueueProducer interface {
	Send(ctx context.Context, slackChannelID, dedupID, body string) error
}

type Server struct {
	rawAlertConsumer  FifoQueueConsumer
	alertQueue        FifoQueueProducer
	limitersByChannel map[string]*rate.Limiter
	limitersLock      *sync.Mutex
	cacheStore        cachestore.StoreInterface
	slackAPI          *slackapi.Client
	channelInfoSyncer *channelInfoSyncer
	logger            common.Logger
	metrics           common.Metrics
	apiSettings       *config.APISettings
	cfg               *config.APIConfig
}

func New(alertQueue FifoQueueProducer, cacheStore cachestore.StoreInterface, logger common.Logger, metrics common.Metrics, cfg *config.APIConfig, settings *config.APISettings) *Server {
	if cacheStore == nil {
		gocacheClient := gocache.New(5*time.Minute, time.Minute)
		cacheStore = gocache_store.NewGoCache(gocacheClient)
	}

	if metrics == nil {
		metrics = &common.NoopMetrics{}
	}

	if settings == nil {
		settings = &config.APISettings{}
	}

	return &Server{
		alertQueue:        alertQueue,
		limitersByChannel: make(map[string]*rate.Limiter),
		limitersLock:      &sync.Mutex{},
		cacheStore:        cacheStore,
		logger:            logger,
		metrics:           metrics,
		apiSettings:       settings,
		cfg:               cfg,
	}
}

// WithRawAlertConsumer defines an alternative alert consumer, which reads from a FIFO queue and processes the items similarly to the main rest API.
// The consumer is started by Run(ctx), and the queue is consumed in a separate goroutine.
// The server can receive alerts from both the main rest API and the raw alert consumer simultaneously.
//
// The queue item body must be a single JSON-serialized common.Alert. Prometheus webhooks are not supported here.
//
// Validation errors are logged, but otherwise ignored (i.e. no retries on bad input).
func (s *Server) WithRawAlertConsumer(consumer FifoQueueConsumer) *Server {
	s.rawAlertConsumer = consumer
	return s
}

// Run starts the HTTP server and handles incoming requests. It also initializes the Slack API client and the channel info syncer.
// If a raw alert consumer is set, it will start a dedicated consumer for that queue.
//
// This method blocks until the context is cancelled, or a server error occurs.
func (s *Server) Run(ctx context.Context) error {
	s.logger.Info("API server started")
	defer s.logger.Infof("API server exited")

	if s.alertQueue == nil {
		return errors.New("alert queue cannot be nil")
	}

	if s.cfg == nil {
		return errors.New("manager configuration cannot be nil")
	}

	if err := s.cfg.Validate(); err != nil {
		return fmt.Errorf("failed to validate manager configuration: %w", err)
	}

	if err := s.apiSettings.InitAndValidate(s.logger); err != nil {
		return fmt.Errorf("failed to initialize API settings: %w", err)
	}

	s.slackAPI = slackapi.New(s.cacheStore, s.cfg.CacheKeyPrefix, s.logger, s.metrics, s.cfg.SlackClient)

	if _, err := s.slackAPI.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to Slack API: %w", err)
	}

	s.channelInfoSyncer = newChannelInfoSyncer(s.slackAPI, s.logger)

	if err := s.channelInfoSyncer.Init(ctx); err != nil {
		return fmt.Errorf("failed to initialize channel info manager: %w", err)
	}

	// Set Gin to release mode for production
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()

	// Add recovery middleware to handle panics
	router.Use(gin.Recovery())

	// Add metrics middleware
	router.Use(s.metricsMiddleware())

	// Add custom logging middleware if logger supports it
	if loggingHandler := s.logger.HttpLoggingHandler(); loggingHandler != nil {
		router.Use(s.loggingMiddleware())
	}

	// We support both /alert and /alerts endpoints for backwards compatibility.
	// Input is either a single alert (common.Alert), or an array of alerts.
	router.POST("/alert", s.handleAlerts)
	router.POST("/alert/:slackChannelId", s.handleAlerts)
	router.POST("/alerts", s.handleAlerts)
	router.POST("/alerts/:slackChannelId", s.handleAlerts)

	// Webhooks from Prometheus alert manager.
	// The alert format is defined in PrometheusWebhook.
	router.POST("/prometheus-alert", s.handlePrometheusWebhook)
	router.POST("/prometheus-alert/:slackChannelId", s.handlePrometheusWebhook)

	// Test endpoint for alerts, which writes the input body as an info log message.
	router.POST("/alerts-test", s.handleAlertsTest)
	router.POST("/alerts-test/:slackChannelId", s.handleAlertsTest)

	// Route mappings
	router.GET("/mappings", s.handleMappings)

	// List channels managed by Slack Manager
	router.GET("/channels", s.handleChannels)

	// Ping
	router.GET("/ping", s.ping)

	readHeaderTimeout := 2 * time.Second
	requestTimeout := s.getRequestTimeout()
	timeoutWiggleRoom := time.Second

	listenAddr := ":" + s.cfg.RestPort

	srv := &http.Server{
		Handler:           router,
		Addr:              listenAddr,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readHeaderTimeout + requestTimeout + timeoutWiggleRoom,
		WriteTimeout:      requestTimeout + timeoutWiggleRoom,
		IdleTimeout:       60 * time.Second,
	}

	s.logger.
		WithField("write_timeout", fmt.Sprintf("%v", srv.WriteTimeout)).
		WithField("read_timeout", fmt.Sprintf("%v", srv.ReadTimeout)).
		WithField("read_header_timeout", fmt.Sprintf("%v", srv.ReadHeaderTimeout)).
		WithField("idle_timeout", fmt.Sprintf("%v", srv.IdleTimeout)).
		WithField("request_timeout", fmt.Sprintf("%v", requestTimeout)).
		WithField("port", s.cfg.RestPort).
		Info("Starting API listener")

	errg, ctx := errgroup.WithContext(ctx)

	if s.rawAlertConsumer != nil {
		errg.Go(func() error {
			return s.runRawAlertConsumer(ctx)
		})
	}

	errg.Go(func() error {
		return s.channelInfoSyncer.Run(ctx)
	})

	errg.Go(func() error {
		return srv.ListenAndServe()
	})

	errg.Go(func() error {
		<-ctx.Done()

		if err := srv.Close(); err != nil {
			s.logger.Errorf("Failed to close http server: %s", err)
		}

		return ctx.Err()
	})

	if err := errg.Wait(); err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}

		return err
	}

	return nil
}

// metricsMiddleware creates a Gin middleware for HTTP request metrics
func (s *Server) metricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		s.metrics.AddHTTPRequestMetric(c.Request.URL.Path, c.Request.Method, c.Writer.Status(), time.Since(start))
	}
}

// loggingMiddleware creates a Gin middleware for HTTP request logging
func (s *Server) loggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		c.Next()

		s.logger.
			WithField("method", c.Request.Method).
			WithField("path", path).
			WithField("status", c.Writer.Status()).
			WithField("latency", time.Since(start).String()).
			WithField("client_ip", c.ClientIP()).
			Debug("HTTP request")
	}
}

func (s *Server) runRawAlertConsumer(ctx context.Context) error {
	s.logger.Info("Starting raw alert consumer")
	defer s.logger.Info("Raw alert consumer exited")

	queueCh := make(chan *common.FifoQueueItem, 100)

	errg, ctx := errgroup.WithContext(ctx)

	errg.Go(func() error {
		return s.rawAlertConsumer.Receive(ctx, queueCh)
	})

	errg.Go(func() error {
		for item := range queueCh {
			logger := s.logger.WithField("message_id", item.MessageID).WithField("slack_channel_id", item.SlackChannelID)
			logger.Debug("Alert received")

			if item.Ack == nil {
				logger.Error("Alert ack function is nil")
				continue
			}

			var alert *common.Alert

			if err := json.Unmarshal([]byte(item.Body), &alert); err != nil {
				logger.Errorf("Failed to json unmarshal queued alert: %s", err)
			} else {
				if err := s.processQueuedAlert(ctx, alert, logger); err != nil {
					logger.Errorf("Failed to process queued alert: %s", err)
					continue
				}
			}

			if err := item.Ack(ctx); err != nil {
				logger.Errorf("Failed to ack queued alert: %s", err)
			}

			logger.Debug("Alert acked")
		}

		return nil
	})

	return errg.Wait()
}

func (s *Server) UpdateSettings(settings *config.APISettings) error {
	if settings == nil {
		settings = &config.APISettings{}
	}

	if err := settings.InitAndValidate(s.logger); err != nil {
		return fmt.Errorf("failed to initialize updated API settings (the existing settings will continue to be used): %w", err)
	}

	s.apiSettings = settings

	s.logger.Infof("API settings updated")

	return nil
}

// writeErrorResponse writes an error response using Gin context
// Note: Metrics are handled by metricsMiddleware, no need to add them here
func (s *Server) writeErrorResponse(c *gin.Context, clientErr error, statusCode int, debugText map[string]string, targetChannel string) {
	errText := clientErr.Error()

	if len(errText) > 1 {
		errText = strings.ToUpper(string(errText[0])) + errText[1:]
	}

	if statusCode < 500 {
		s.logger.Infof("Request failed: %s", errText)
	} else {
		s.logger.Errorf("Request failed: %s", errText)
	}

	// Use plain text response for backward compatibility
	c.String(statusCode, errText)

	if s.cfg.ErrorReportChannelID != "" {
		alert := s.createClientErrorAlert(clientErr, statusCode, debugText, targetChannel)

		if err := s.queueAlert(c.Request.Context(), alert); err != nil {
			s.logger.Errorf("Failed to queue client error alert: %s", err)
		}
	}
}

func (s *Server) queueAlert(ctx context.Context, alert *common.Alert) error {
	if alert.SlackChannelID == "" {
		return errors.New("alert has no Slack channel ID")
	}

	body, err := json.Marshal(alert)
	if err != nil {
		return fmt.Errorf("failed to marshal alert: %w", err)
	}

	if err := s.alertQueue.Send(ctx, alert.SlackChannelID, alert.DedupID(), string(body)); err != nil {
		return fmt.Errorf("failed to send message to queue: %w", err)
	}

	return nil
}

func (s *Server) waitForRateLimit(ctx context.Context, channel string, count int, failOnRateLimitError bool) (int, error) {
	limiter := s.getRateLimiter(channel)
	attempt := 1

	for {
		timeout := time.Duration(s.cfg.RateLimit.MaxWaitPerAttemptSeconds) * time.Second
		ctx, cancel := context.WithTimeout(ctx, timeout)

		defer cancel()

		err := limiter.WaitN(ctx, count)

		if err == nil {
			return count, nil
		}

		// Something other than rate limiting is going on
		if !strings.Contains(err.Error(), "rate") {
			return 0, fmt.Errorf("failed to wait for rate limit: %w", err)
		}

		// Stop trying after configured number of events
		if attempt >= s.cfg.RateLimit.MaxAttempts {
			return 0, fmt.Errorf("rate limit exceeded for %d alerts in channel %s", count, channel)
		}

		attempt++

		// Reduce the number of permits to wait for, IF failOnRateLimitError is false
		if count > 3 && !failOnRateLimitError {
			count /= 2
		}
	}
}

func (s *Server) getRateLimiter(channel string) *rate.Limiter {
	s.limitersLock.Lock()
	defer s.limitersLock.Unlock()

	limiter, ok := s.limitersByChannel[channel]

	if !ok {
		limiter = rate.NewLimiter(rate.Limit(s.cfg.RateLimit.AlertsPerSecond), s.cfg.RateLimit.AllowedBurst)
		s.limitersByChannel[channel] = limiter
	}

	return limiter
}

func (s *Server) debugLogRequest(req *http.Request, body []byte) {
	s.logger.WithField("body", string(body)).Debugf("%s %s", req.Method, req.URL.Path)
}

func (s *Server) getRequestTimeout() time.Duration {
	apiRateLimitMaxTimeSeconds := s.cfg.RateLimit.MaxWaitPerAttemptSeconds * s.cfg.RateLimit.MaxAttempts

	timeoutSeconds := max(
		apiRateLimitMaxTimeSeconds,
		s.cfg.SlackClient.MaxRateLimitErrorWaitTimeSeconds,
		s.cfg.SlackClient.MaxTransientErrorWaitTimeSeconds,
		s.cfg.SlackClient.MaxFatalErrorWaitTimeSeconds,
	)

	return time.Duration(timeoutSeconds+1) * time.Second
}

func max(values ...int) int {
	max := 0

	for _, v := range values {
		if v > max {
			max = v
		}
	}

	return max
}
