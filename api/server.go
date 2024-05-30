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
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	gocache "github.com/patrickmn/go-cache"
	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/peteraglen/slack-manager/internal/slackapi"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
)

type FifoQueueProducer interface {
	Send(ctx context.Context, slackChannelID, dedupID, body string) error
}

type Server struct {
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

// Run starts the server and handles incoming requests. This method blocks until the context is cancelled, or a server error occurs.
func (s *Server) Run(ctx context.Context) error {
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
		Infof("API available on port %s", s.cfg.RestPort)

	errg, ctx := errgroup.WithContext(ctx)

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

	return nil
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

	s.metrics.AddHTTPRequestMetric(req.URL.Path, req.Method, statusCode, time.Since(started))

	if s.cfg.ErrorReportChannelID != "" {
		alert := s.createClientErrorAlert(clientErr, statusCode, debugText, targetChannel)

		if err := s.queueAlert(ctx, alert); err != nil {
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
