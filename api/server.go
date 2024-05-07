package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/eko/gocache/lib/v4/cache"
	cachestore "github.com/eko/gocache/lib/v4/store"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/peteraglen/slack-manager/client"
	"github.com/peteraglen/slack-manager/common"
	"github.com/slack-go/slack"
	"golang.org/x/time/rate"
)

type SlackAPI interface {
	GetChannelInfo(ctx context.Context, channelID string) (*slack.Channel, error)
	ListBotChannels(ctx context.Context) ([]*common.ChannelSummary, error)
	GetUserIDsInChannel(ctx context.Context, channelID string) (map[string]struct{}, error)
	BotIsInChannel(ctx context.Context, channelID string) (bool, error)
}

type Server struct {
	alertQueue         common.FifoQueueProducer
	limitersByChannel  map[string]*rate.Limiter
	limitersLock       *sync.Mutex
	config             *Config
	cache              *common.Cache[string]
	channelInfoManager *channelInfoManager
	metrics            common.Metrics
	logger             common.Logger
}

func New(ctx context.Context, slackAPI SlackAPI, alertQueue common.FifoQueueProducer, cacheStore cachestore.StoreInterface, metrics common.Metrics, logger common.Logger, config *Config) (*Server, error) {
	channelInfoManager := newChannelInfoManager(slackAPI, logger)

	if err := channelInfoManager.Init(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize channel info manager: %w", err)
	}

	cache := common.NewCache(cache.New[string](cacheStore), logger)

	return &Server{
		alertQueue:         alertQueue,
		config:             config,
		limitersByChannel:  make(map[string]*rate.Limiter),
		limitersLock:       &sync.Mutex{},
		cache:              cache,
		channelInfoManager: channelInfoManager,
		metrics:            metrics,
		logger:             logger,
	}, nil
}

// Run starts the server and handles incoming requests. This method blocks until the context is cancelled, or a server error occurs.
func (s *Server) Run(ctx context.Context) error {
	// The channel info manager runs until the context is cancelled
	go s.channelInfoManager.Run(ctx)

	router := mux.NewRouter()

	// Stateful alerts
	router.HandleFunc("/alert", s.alert).Methods("POST")
	router.HandleFunc("/alert/{slackChannelId}", s.alert).Methods("POST")
	router.HandleFunc("/alerts", s.alerts).Methods("POST")
	router.HandleFunc("/alerts/{slackChannelId}", s.alerts).Methods("POST")

	// Prometheus alert manager
	router.HandleFunc("/prometheus-alert", s.prometheusAlert).Methods("POST")
	router.HandleFunc("/prometheus-alert/{slackChannelId}", s.prometheusAlert).Methods("POST")

	// Test endpoint for stateful alerts, which writes the input message as an info log message
	router.HandleFunc("/alerts-test", s.testSlackAlertsHandler).Methods("POST")
	router.HandleFunc("/alerts-test/{slackChannelId}", s.testSlackAlertsHandler).Methods("POST")

	// Route mappings
	router.HandleFunc("/mappings", s.mappings).Methods("GET")

	// List channels managed by SUDO Slack Manager
	router.HandleFunc("/channels", s.channels).Methods("GET")

	// Ping
	router.HandleFunc("/ping", s.ping).Methods("GET")

	readHeaderTimeout := 2 * time.Second
	requestTimeout := s.config.GetRecommendedRequestTimeout()
	timeoutWiggleRoom := time.Second

	var handler http.Handler

	if loggingHandler := s.logger.HttpLoggingHandler(); loggingHandler != nil {
		handler = handlers.LoggingHandler(loggingHandler, router)
	} else {
		handler = router
	}

	timeoutHandler := http.TimeoutHandler(handler, requestTimeout, "Handler timeout")
	listenAddr := fmt.Sprintf(":%s", s.config.RestPort)

	srv := &http.Server{
		Handler:           timeoutHandler,
		Addr:              listenAddr,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readHeaderTimeout + requestTimeout + timeoutWiggleRoom,
		WriteTimeout:      requestTimeout + timeoutWiggleRoom,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		if err := srv.Close(); err != nil {
			s.logger.Errorf("Failed to close http server: %s", err)
		}
	}()

	s.logger.
		WithField("write_timeout", fmt.Sprintf("%v", srv.WriteTimeout)).
		WithField("read_timeout", fmt.Sprintf("%v", srv.ReadTimeout)).
		WithField("read_header_timeout", fmt.Sprintf("%v", srv.ReadHeaderTimeout)).
		WithField("idle_timeout", fmt.Sprintf("%v", srv.IdleTimeout)).
		WithField("request_timeout", fmt.Sprintf("%v", requestTimeout)).
		Infof("API available on port %s", s.config.RestPort)

	err := srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	if err != nil {
		return fmt.Errorf("HTTP listener on port %s failed with error: %w", s.config.RestPort, err)
	}

	return nil
}

func (s *Server) mappings(resp http.ResponseWriter, req *http.Request) {
	started := time.Now()

	mappings := s.config.GetAlertMappings()

	data, err := json.MarshalIndent(mappings, "", "  ")
	if err != nil {
		err = fmt.Errorf("failed to marshal alert mappings: %w", err)
		s.writeErrorResponse(req.Context(), err, http.StatusInternalServerError, nil, "", resp, req, started)
		return
	}

	resp.Header().Add("Content-Type", "application/json")
	resp.WriteHeader(http.StatusOK)

	_, err = resp.Write(data)
	if err != nil {
		s.logger.Errorf("Failed to write mappings response: %s", err)
	}

	s.metrics.AddHttpRequestMetric(req.URL.Path, req.Method, http.StatusOK, time.Since(started))
}

func (s *Server) channels(resp http.ResponseWriter, req *http.Request) {
	started := time.Now()

	channels := s.channelInfoManager.ManagedChannels()

	data, err := json.MarshalIndent(channels, "", "  ")
	if err != nil {
		err = fmt.Errorf("failed to marshal channel list: %w", err)
		s.writeErrorResponse(req.Context(), err, http.StatusInternalServerError, nil, "", resp, req, started)
		return
	}

	resp.Header().Add("Content-Type", "application/json")
	resp.WriteHeader(http.StatusOK)

	_, err = resp.Write(data)
	if err != nil {
		s.logger.Errorf("Failed to write channel list response: %s", err)
	}

	s.metrics.AddHttpRequestMetric(req.URL.Path, req.Method, http.StatusOK, time.Since(started))
}

func (s *Server) ping(resp http.ResponseWriter, req *http.Request) {
	started := time.Now()

	resp.Header().Add("Content-Type", "text/plain")
	resp.WriteHeader(http.StatusOK)

	_, err := resp.Write([]byte("pong"))
	if err != nil {
		s.logger.Errorf("Failed to write ping response: %s", err)
	}

	s.metrics.AddHttpRequestMetric(req.URL.Path, req.Method, http.StatusOK, time.Since(started))
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

	s.metrics.AddHttpRequestMetric(req.URL.Path, req.Method, statusCode, time.Since(started))

	if s.config.ErrorReportChannelID != "" {
		alert := s.createClientErrorAlert(clientErr, statusCode, debugText, targetChannel)

		if err := s.queueAlert(ctx, alert); err != nil {
			s.logger.Errorf("Failed to queue client error alert: %s", err)
		}
	}
}

func (s *Server) queueAlert(ctx context.Context, alert *client.Alert) error {
	body, err := json.Marshal(alert)
	if err != nil {
		return fmt.Errorf("failed to marshal alert: %w", err)
	}

	groupID := alert.SlackChannelID
	dedupID := common.Hash("alert", alert.SlackChannelID, alert.CorrelationID, alert.Timestamp.Format(time.RFC3339Nano))

	if err := s.alertQueue.Send(ctx, groupID, dedupID, string(body)); err != nil {
		return fmt.Errorf("failed to send alert to SQS queue: %w", err)
	}

	return nil
}

func (s *Server) waitForRateLimit(ctx context.Context, channel string, count int, failOnRateLimitError bool) (int, error) {
	limiter := s.getRateLimiter(channel)
	attempt := 1

	for {
		ctx, cancel := context.WithTimeout(ctx, s.config.RateLimit.MaxWaitPerAttempt)

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
		if attempt >= s.config.RateLimit.MaxAttempts {
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
		limiter = rate.NewLimiter(rate.Limit(s.config.RateLimit.AlertsPerSecond), s.config.RateLimit.AllowedBurst)
		s.limitersByChannel[channel] = limiter
	}

	return limiter
}

func (s *Server) debugLogRequest(req *http.Request, body []byte) {
	s.logger.WithField("body", string(body)).Debugf("%s %s", req.Method, req.URL.Path)
}
