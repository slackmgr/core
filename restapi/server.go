package restapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	cachestore "github.com/eko/gocache/lib/v4/store"
	gocache_store "github.com/eko/gocache/store/go_cache/v4"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	gocache "github.com/patrickmn/go-cache"
	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/peteraglen/slack-manager/internal/slackapi"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
)

const (
	httpRequestMetric = "http_server_request_duration_seconds"
)

type FifoQueueConsumer interface {
	Receive(ctx context.Context, sinkCh chan<- *common.FifoQueueItem) error
}

type FifoQueueProducer interface {
	Send(ctx context.Context, slackChannelID, dedupID, body string) error
}

type Server struct {
	rawAlertConsumers []FifoQueueConsumer
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
//
// Multiple raw alert consumers can be added.
//
// The server can receive alerts from both the main rest API and the raw alert consumers simultaneously.
//
// The queue item body must be a single JSON-serialized common.Alert. Prometheus webhooks are not supported here.
func (s *Server) WithRawAlertConsumer(consumer FifoQueueConsumer) *Server {
	s.rawAlertConsumers = append(s.rawAlertConsumers, consumer)
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

	// Register prometheus histogram metric for HTTP request durations
	metricsLabels := []string{"path", "method", "status"}
	s.metrics.RegisterHistogram(httpRequestMetric, "The duration of incoming HTTP server requests in seconds",
		[]float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}, metricsLabels...)

	router := mux.NewRouter()

	// We support both /alert and /alerts endpoints for backwards compatibility.
	// Input is either a single alert (common.Alert), or an array of alerts.
	router.HandleFunc("/alert", s.handleAlerts).Methods("POST")
	router.HandleFunc("/alert/{slackChannelId}", s.handleAlerts).Methods("POST")
	router.HandleFunc("/alerts", s.handleAlerts).Methods("POST")
	router.HandleFunc("/alerts/{slackChannelId}", s.handleAlerts).Methods("POST")

	// Webhooks from Prometheus alert manager.
	// The alert format is defined in PrometheusWebhook.
	router.HandleFunc("/prometheus-alert", s.handlePrometheusWebhook).Methods("POST")
	router.HandleFunc("/prometheus-alert/{slackChannelId}", s.handlePrometheusWebhook).Methods("POST")

	// Test endpoint for alerts, which writes the input body as an info log message.
	router.HandleFunc("/alerts-test", s.handleAlertsTest).Methods("POST")
	router.HandleFunc("/alerts-test/{slackChannelId}", s.handleAlertsTest).Methods("POST")

	// Route mappings
	router.HandleFunc("/mappings", s.handleMappings).Methods("GET")

	// List channels managed by Slack Manager
	router.HandleFunc("/channels", s.handleChannels).Methods("GET")

	// Ping
	router.HandleFunc("/ping", s.ping).Methods("GET")

	readHeaderTimeout := 2 * time.Second
	requestTimeout := s.getRequestTimeout()
	timeoutWiggleRoom := time.Second

	var handler http.Handler

	if loggingHandler := s.logger.HttpLoggingHandler(); loggingHandler != nil {
		handler = handlers.LoggingHandler(loggingHandler, router)
	} else {
		handler = router
	}

	timeoutHandler := http.TimeoutHandler(handler, requestTimeout, "Handler timeout")
	listenAddr := ":" + s.cfg.RestPort

	srv := &http.Server{
		Handler:           timeoutHandler,
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

	// Start each alert consumer, if any.
	for _, consumer := range s.rawAlertConsumers {
		errg.Go(func() error {
			return s.runRawAlertConsumer(ctx, consumer)
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

// runRawAlertConsumer starts consuming alerts from the given FIFO queue consumer.
// Each alert is expected to be a JSON-serialized common.Alert.
//
// Retryable processing errors result in the message being nacked, thus allowing re-processing later.
// Non-retryable processing errors result in the message being acked, thus avoiding re-processing.
//
// This method blocks until the context is cancelled, i.e. the consumer.Receive() method exits.
func (s *Server) runRawAlertConsumer(ctx context.Context, consumer FifoQueueConsumer) error {
	s.logger.Info("Starting raw alert consumer")
	defer s.logger.Info("Raw alert consumer exited")

	queueCh := make(chan *common.FifoQueueItem, 100)

	errg, ctx := errgroup.WithContext(ctx)

	errg.Go(func() error {
		return consumer.Receive(ctx, queueCh)
	})

	errg.Go(func() error {
		for item := range queueCh {
			logger := s.logger.WithField("message_id", item.MessageID).WithField("channel_id", item.SlackChannelID)
			logger.Debug("Alert received")

			var alert *common.Alert

			// Unmarshal the alert from the queue item body.
			// If the alert is invalid, we *ack* the message to avoid re-processing it.
			if err := json.Unmarshal([]byte(item.Body), &alert); err != nil {
				logger.Errorf("Failed to json unmarshal queued alert: %s", err)

				if item.Ack != nil {
					item.Ack(ctx)
					logger.Debug("Alert acked")
				}

				continue
			}

			// Process the alert.
			// If processing fails with a retryable error, we *nack* the message, thus allowing it to be re-processed later.
			if err := s.processQueuedAlert(ctx, alert); err != nil {
				logger.Errorf("Failed to process queued alert: %s", err)

				var pErr *processingError

				if errors.As(err, &pErr) && pErr.IsRetryable() {
					if item.Nack != nil {
						item.Nack(ctx)
						logger.Debug("Alert nacked")
					}

					continue
				}
			}

			// At this point, the alert has been processed successfully OR it failed with a non-retryable error.
			// In both cases, we ack the message to avoid re-processing it.
			if item.Ack != nil {
				item.Ack(ctx)
				logger.Debug("Alert acked")
			}
		}

		return nil
	})

	return errg.Wait()
}

func (s *Server) writeErrorResponse(ctx context.Context, clientErr error, statusCode int, debugText map[string]string, targetChannel string, resp http.ResponseWriter, req *http.Request, started time.Time) {
	errText := clientErr.Error()

	if len(errText) > 1 {
		errText = strings.ToUpper(string(errText[0])) + errText[1:]
	}

	if statusCode < 500 {
		s.logger.Infof("Request failed: %s", errText)
	} else {
		s.logger.Errorf("Request failed: %s", errText)
	}

	resp.Header().Add("Content-Type", "text/plain")
	resp.WriteHeader(statusCode)

	if _, err := resp.Write([]byte(errText)); err != nil {
		s.logger.Errorf("Failed to write error response: %s", err)
	}

	s.metrics.Observe(httpRequestMetric, time.Since(started).Seconds(), req.URL.Path, req.Method, strconv.Itoa(statusCode))

	if s.cfg.ErrorReportChannelID != "" {
		alert := s.createClientErrorAlert(clientErr, statusCode, debugText, targetChannel)

		if err := s.queueAlert(ctx, alert); err != nil {
			s.logger.Errorf("Failed to queue client error alert: %s", err)
		}
	}
}

// queueAlert serializes the alert and sends it to the alert queue.
// Any returned errors are considered retryable, as they typically indicate transient queuing issues.
func (s *Server) queueAlert(ctx context.Context, alert *common.Alert) error {
	// Serialize the alert to JSON
	// This really can't fail, but if it does, we log the error and return nil.
	// Errors from this method are supposed to indicate retryable queuing errors, not fatal serialization errors.
	body, err := json.Marshal(alert)
	if err != nil {
		s.logger.Errorf("Failed to marshal alert: %w", err)
		return nil
	}

	// Send the alert to the queue. Any errors here are retryable and thus returned.
	if err := s.alertQueue.Send(ctx, alert.SlackChannelID, alert.UniqueID(), string(body)); err != nil {
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
