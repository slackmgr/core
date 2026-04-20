package internal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	cachestore "github.com/eko/gocache/lib/v4/store"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
	"github.com/slackmgr/core/config"
	"github.com/slackmgr/types"
	"golang.org/x/sync/semaphore"
)

const (
	// SlackChannelNotFoundError is returned when the target channel does not exist or
	// the bot lacks visibility of it. Non-retryable.
	SlackChannelNotFoundError = "channel_not_found"

	// SlackThreadNotFoundError is returned when the target thread does not exist,
	// typically because the parent message was deleted. Non-retryable.
	SlackThreadNotFoundError = "thread_not_found"

	// SlackNoSuchSubTeamError is returned when a referenced user group does not exist
	// in the workspace. Non-retryable.
	SlackNoSuchSubTeamError = "no_such_subteam"

	// slackCantUpdateMessageError is returned when the bot attempts to edit a message
	// it is not permitted to modify (e.g. another user's message). Non-retryable.
	slackCantUpdateMessageError = "cant_update_message"

	// slackCantDeleteMessageError is returned when the bot attempts to delete a message
	// it is not permitted to remove (e.g. another user's message). Non-retryable.
	slackCantDeleteMessageError = "cant_delete_message"

	// slackMessageNotFoundError is returned when the target message does not exist,
	// typically because it was already deleted. Non-retryable.
	slackMessageNotFoundError = "message_not_found"

	// slackNotInChannelError is returned when the bot is not a member of the channel
	// it is trying to interact with. Non-retryable.
	slackNotInChannelError = "not_in_channel"

	// slackInvalidBlocksError is returned when a message payload contains malformed
	// Block Kit JSON. Non-retryable.
	slackInvalidBlocksError = "invalid_blocks"

	// slackIsArchivedError is returned when the target channel has been archived and
	// no longer accepts messages. Non-retryable.
	slackIsArchivedError = "is_archived"

	// slackRestrictedActionError is returned when a workspace policy prevents the
	// requested action (e.g. posting is restricted to certain roles). Non-retryable.
	slackRestrictedActionError = "restricted_action"

	// slackInternalError is a transient server-side error returned by Slack when
	// something went wrong on their end. Safe to retry.
	slackInternalError = "internal_error"

	// slackFatalError is a transient error string returned by Slack for unspecified
	// server-side failures. Despite the name, Slack treats this as retryable.
	slackFatalError = "fatal_error"

	// slackServiceUnavailableError is a transient error returned when the Slack API
	// is temporarily unavailable. Safe to retry.
	slackServiceUnavailableError = "service_unavailable"

	// slackRequestTimeoutError is a transient error returned when Slack's own
	// processing timed out handling the request. Safe to retry.
	slackRequestTimeoutError = "request_timeout"

	// slackRateLimitedError is the error string Slack returns alongside a 429 response.
	// The slack-go library also surfaces this as *slack.RateLimitedError, but the string
	// form can appear in other contexts. Safe to retry after the Retry-After delay.
	slackRateLimitedError = "ratelimited"

	// slackClientCacheRequestsTotalMetric counts calls to the cacheable Slack client wrapper
	// methods, labelled by result ("hit" = served from cache, "miss" = required an API call).
	slackClientCacheRequestsTotalMetric = "slack_client_cache_requests_total"

	// slackAPICallsTotalMetric counts actual outbound HTTP calls made to the Slack API.
	slackAPICallsTotalMetric = "slack_api_calls_total"

	// slackAPIErrorsTotalMetric counts Slack API call errors, labelled by error_type.
	slackAPIErrorsTotalMetric = "slack_api_errors_total"

	// slackAPIRetriesTotalMetric counts retry attempts after a recoverable Slack API error.
	slackAPIRetriesTotalMetric = "slack_api_retries_total"

	// slackAPIRateLimitDurationMetric is a histogram of Retry-After durations from Slack
	// 429 responses — how many seconds the server asked us to back off.
	slackAPIRateLimitDurationMetric = "slack_api_rate_limit_duration_seconds"
)

// ErrNotConnected is returned when an API method is called before Connect().
var ErrNotConnected = errors.New("client not connected: Connect() must be called first")

type retryable interface{ Retryable() bool }

// rateLimitSignaler is satisfied by any type that can record a rate-limit
// deadline so that all callers can back off until it expires.
type rateLimitSignaler interface {
	Signal(ctx context.Context, until time.Time) error
}

type SlackAPIClient struct {
	api       *slack.Client
	logger    types.Logger
	cache     *Cache
	metrics   types.Metrics
	cfg       *config.SlackClientConfig
	gate      rateLimitSignaler   // nil = no distributed signalling
	sem       *semaphore.Weighted // limits concurrent Slack API calls
	connected bool
	botUserID string
}

// WithRateLimitGate is a functional option that wires a RateLimitSignaler into
// the SlackAPIClient so that a detected 429 is broadcast to all callers.
func WithRateLimitGate(g rateLimitSignaler) func(*SlackAPIClient) {
	return func(c *SlackAPIClient) { c.gate = g }
}

func NewSlackAPIClient(cacheStore cachestore.StoreInterface, cacheKeyPrefix string, logger types.Logger, metrics types.Metrics, cfg *config.SlackClientConfig, opts ...func(*SlackAPIClient)) *SlackAPIClient {
	cfg.SetDefaults()

	cacheKeyPrefix += "slack-api-client:"
	cache := NewCache(cacheStore, cacheKeyPrefix, logger)

	c := &SlackAPIClient{
		logger:  logger,
		cache:   cache,
		metrics: metrics,
		cfg:     cfg,
		sem:     semaphore.NewWeighted(int64(cfg.Concurrency)),
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

func (c *SlackAPIClient) Connect(ctx context.Context) (*slack.AuthTestResponse, error) {
	if c.connected {
		return nil, errors.New("connect can only be run once")
	}

	if c.metrics == nil {
		return nil, errors.New("client metrics cannot be nil")
	}

	if c.cache == nil {
		return nil, errors.New("client cache cannot be nil")
	}

	if c.cfg == nil {
		return nil, errors.New("client options cannot be nil")
	}

	if c.cfg.BotToken == "" {
		return nil, errors.New("client bot token cannot be nil")
	}

	c.metrics.RegisterCounter(slackClientCacheRequestsTotalMetric, "Total calls to cacheable Slack client wrapper methods, by action and result", "slack_action", "result")
	c.metrics.RegisterCounter(slackAPICallsTotalMetric, "Total outbound HTTP calls made to the Slack API", "slack_action")

	for _, action := range []string{"conversations.info", "users.conversations", "users.info", "usergroups.users.list", "conversations.members"} {
		for _, result := range []string{"hit", "miss"} {
			c.metrics.CounterAdd(slackClientCacheRequestsTotalMetric, 0, action, result)
		}
	}
	c.metrics.RegisterCounter(slackAPIErrorsTotalMetric, "Total Slack API call errors by error type", "slack_action", "error_type")
	c.metrics.RegisterCounter(slackAPIRetriesTotalMetric, "Total Slack API retry attempts after a recoverable error", "slack_action", "error_type")
	c.metrics.RegisterHistogram(slackAPIRateLimitDurationMetric, "Retry-After duration in seconds from Slack 429 responses", []float64{1, 5, 10, 30, 60, 120, 300})

	httpClient := &http.Client{
		Timeout: time.Duration(c.cfg.HTTPTimeoutSeconds) * time.Second,
	}

	c.api = slack.New(
		c.cfg.BotToken,
		slack.OptionDebug(c.cfg.DebugLogging),
		slack.OptionAppLevelToken(c.cfg.AppToken),
		slack.OptionLog(&slackApilogger{logger: c.logger}),
		slack.OptionHTTPClient(httpClient),
	)

	response, err := c.api.AuthTestContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed Slack authorization test: %w", err)
	}

	if response.UserID == "" {
		return nil, errors.New("missing bot user ID in auth test response")
	}

	c.botUserID = response.UserID

	c.logger.Infof("Slack authorization tested OK with bot user %s (%s)", response.User, response.UserID)

	c.connected = true

	return response, nil
}

func (c *SlackAPIClient) API() *slack.Client {
	return c.api
}

func (c *SlackAPIClient) NewSocketModeClient() *SocketModeClientWrapper {
	s := socketmode.New(c.api, socketmode.OptionDebug(c.cfg.DebugLogging), socketmode.OptionLog(&slackApilogger{logger: c.logger}))
	return NewSocketModeClientWrapper(s, c.logger)
}

func (c *SlackAPIClient) BotUserID() string {
	return c.botUserID
}

func (c *SlackAPIClient) ChatPostMessage(ctx context.Context, channelID string, options ...slack.MsgOption) (string, error) {
	if err := c.checkConnected(); err != nil {
		return "", err
	}

	action := "chat.post"

	f := func(ctx context.Context) (string, any, error) {
		_, ts, _, err := c.api.SendMessageContext(ctx, channelID, options...)
		return ts, nil, err
	}

	ts, _, err := callAPI(ctx, c.logger.WithField("channel_id", channelID), c.metrics, c.cfg, c.gate, c.sem, action, f)

	return ts, err
}

func (c *SlackAPIClient) ChatUpdateMessage(ctx context.Context, channelID string, options ...slack.MsgOption) (string, error) {
	if err := c.checkConnected(); err != nil {
		return "", err
	}

	action := "chat.update"

	f := func(ctx context.Context) (string, any, error) {
		_, ts, _, err := c.api.SendMessageContext(ctx, channelID, options...)
		return ts, nil, err
	}

	ts, _, err := callAPI(ctx, c.logger.WithField("channel_id", channelID), c.metrics, c.cfg, c.gate, c.sem, action, f)

	return ts, err
}

func (c *SlackAPIClient) ChatDeleteMessage(ctx context.Context, channelID string, ts string) error {
	if err := c.checkConnected(); err != nil {
		return err
	}

	action := "chat.delete"

	f := func(ctx context.Context) (any, any, error) {
		_, _, _, err := c.api.SendMessageContext(ctx, channelID, slack.MsgOptionDelete(ts))
		return nil, nil, err
	}

	_, _, err := callAPI(ctx, c.logger.WithField("channel_id", channelID), c.metrics, c.cfg, c.gate, c.sem, action, f)

	return err
}

func (c *SlackAPIClient) SendResponse(ctx context.Context, channelID, responseURL, responseType, text string) error {
	if err := c.checkConnected(); err != nil {
		return err
	}

	if channelID == "" {
		return errors.New("channelID cannot be empty")
	}

	if responseURL == "" {
		return errors.New("responseURL cannot be empty")
	}

	if responseType == "" {
		return errors.New("responseType cannot be empty")
	}

	if text == "" {
		return errors.New("text cannot be empty")
	}

	action := "chat.responseURL"

	options := []slack.MsgOption{slack.MsgOptionResponseURL(responseURL, responseType), slack.MsgOptionText(text, false)}

	f := func(ctx context.Context) (any, any, error) {
		_, _, _, err := c.api.SendMessageContext(ctx, channelID, options...)
		return nil, nil, err
	}

	_, _, err := callAPI(ctx, c.logger.WithField("channel_id", channelID), c.metrics, c.cfg, c.gate, c.sem, action, f)

	return err
}

func (c *SlackAPIClient) PostEphemeral(ctx context.Context, channelID, userID string, options ...slack.MsgOption) (string, error) {
	if err := c.checkConnected(); err != nil {
		return "", err
	}

	if channelID == "" {
		return "", errors.New("channelID cannot be empty")
	}

	if userID == "" {
		return "", errors.New("userID cannot be empty")
	}

	action := "chat.postEphemeral"

	f := func(ctx context.Context) (string, any, error) {
		ts, err := c.api.PostEphemeralContext(ctx, channelID, userID, options...)
		return ts, nil, err
	}

	ts, _, err := callAPI(ctx, c.logger.WithField("channel_id", channelID), c.metrics, c.cfg, c.gate, c.sem, action, f)

	return ts, err
}

func (c *SlackAPIClient) OpenModal(ctx context.Context, triggerID string, request slack.ModalViewRequest) error {
	if err := c.checkConnected(); err != nil {
		return err
	}

	if triggerID == "" {
		return errors.New("triggerID cannot be empty")
	}

	_, err := c.api.OpenViewContext(ctx, triggerID, request)

	return err
}

func (c *SlackAPIClient) MessageHasReplies(ctx context.Context, channelID, ts string) (bool, error) {
	if err := c.checkConnected(); err != nil {
		return false, err
	}

	if channelID == "" {
		return false, errors.New("channelID cannot be empty")
	}

	if ts == "" {
		return false, errors.New("ts cannot be empty")
	}

	action := "conversations.replies"

	params := &slack.GetConversationRepliesParameters{
		ChannelID: channelID,
		Timestamp: ts,
		Inclusive: true,
		Limit:     3,
	}

	f := func(ctx context.Context) ([]slack.Message, any, error) {
		msgs, _, _, err := c.api.GetConversationRepliesContext(ctx, params)
		return msgs, nil, err
	}

	msgs, _, err := callAPI(ctx, c.logger.WithField("channel_id", channelID), c.metrics, c.cfg, c.gate, c.sem, action, f, SlackChannelNotFoundError, SlackThreadNotFoundError)
	if err != nil {
		if err.Error() == SlackChannelNotFoundError || err.Error() == SlackThreadNotFoundError {
			return false, nil
		}
		return false, err
	}

	return len(msgs) > 1, nil
}

func (c *SlackAPIClient) GetChannelInfo(ctx context.Context, channelID string) (*slack.Channel, error) {
	if err := c.checkConnected(); err != nil {
		return nil, err
	}

	if channelID == "" {
		return nil, errors.New("channelID cannot be empty")
	}

	action := "conversations.info"

	cacheKey := "GetChannelInfo:" + channelID

	if val, hit := c.cache.Get(ctx, cacheKey); hit {
		c.metrics.CounterInc(slackClientCacheRequestsTotalMetric, action, "hit")

		// If we cached a ChannelNotFoundError, return it as an error
		if val == SlackChannelNotFoundError {
			return nil, errors.New(SlackChannelNotFoundError)
		}

		info := slack.Channel{}

		if err := json.Unmarshal([]byte(val), &info); err != nil {
			c.logger.WithField("channel_id", channelID).Errorf("Failed to json unmarshal channel info: %s", err)
		} else {
			return &info, nil
		}
	}

	c.metrics.CounterInc(slackClientCacheRequestsTotalMetric, action, "miss")

	f := func(ctx context.Context) (*slack.Channel, any, error) {
		input := slack.GetConversationInfoInput{
			ChannelID:     channelID,
			IncludeLocale: false,
		}
		val, err := c.api.GetConversationInfoContext(ctx, &input)
		return val, nil, err
	}

	channel, _, err := callAPI(ctx, c.logger.WithField("channel_id", channelID), c.metrics, c.cfg, c.gate, c.sem, action, f, SlackChannelNotFoundError)
	unknownChannel := false

	if err != nil {
		if err.Error() == SlackChannelNotFoundError {
			unknownChannel = true
		} else {
			return nil, err
		}
	}

	// No channel found, cache the error to avoid repeated API calls, and then return the error
	if unknownChannel {
		c.cache.SetWithRandomExpiration(ctx, cacheKey, SlackChannelNotFoundError, 10*time.Second, 10*time.Second)
		return nil, err
	}

	// Otherwise cache and return the channel info
	resultJSON, err := json.Marshal(channel)
	if err != nil {
		c.logger.WithField("channel_id", channelID).Errorf("Failed to json marshal channel info: %s", err)
	} else {
		c.cache.SetWithRandomExpiration(ctx, cacheKey, string(resultJSON), 10*time.Second, 10*time.Second)
	}

	return channel, nil
}

func (c *SlackAPIClient) ListBotChannels(ctx context.Context) ([]*ChannelSummary, error) {
	if err := c.checkConnected(); err != nil {
		return nil, err
	}

	action := "users.conversations"

	cacheKey := "ListBotChannels"

	if val, hit := c.cache.Get(ctx, cacheKey); hit {
		c.metrics.CounterInc(slackClientCacheRequestsTotalMetric, action, "hit")

		channels := []*ChannelSummary{}

		if err := json.Unmarshal([]byte(val), &channels); err != nil {
			c.logger.Errorf("failed to json unmarshal channel list: %s", err)
		} else {
			return channels, nil
		}
	}

	c.metrics.CounterInc(slackClientCacheRequestsTotalMetric, action, "miss")

	params := &slack.GetConversationsForUserParameters{
		ExcludeArchived: true,
		Limit:           999,
		Types:           []string{"public_channel", "private_channel"},
	}

	f := func(ctx context.Context) ([]slack.Channel, string, error) {
		return c.api.GetConversationsForUserContext(ctx, params)
	}

	channels := []*ChannelSummary{}

	for {
		chs, nextCursor, err := callAPI(ctx, c.logger, c.metrics, c.cfg, c.gate, c.sem, action, f)
		if err != nil {
			return nil, err
		}

		for _, ch := range chs {
			channels = append(channels, NewChannelSummary(ch))
		}

		if nextCursor == "" {
			break
		}

		params.Cursor = nextCursor
	}

	slices.SortFunc(channels, func(a, b *ChannelSummary) int {
		return strings.Compare(a.Name, b.Name)
	})

	resultJSON, err := json.Marshal(channels)
	if err != nil {
		c.logger.Errorf("failed to json marshal channel list: %s", err)
	} else {
		c.cache.Set(ctx, cacheKey, string(resultJSON), time.Minute)
	}

	return channels, nil
}

func (c *SlackAPIClient) GetUserInfo(ctx context.Context, userID string) (*slack.User, error) {
	if err := c.checkConnected(); err != nil {
		return nil, err
	}

	if userID == "" {
		return nil, errors.New("userID cannot be empty")
	}

	action := "users.info"

	cacheKey := "GetUserInfo:" + userID

	if val, hit := c.cache.Get(ctx, cacheKey); hit {
		c.metrics.CounterInc(slackClientCacheRequestsTotalMetric, action, "hit")

		user := slack.User{}

		if err := json.Unmarshal([]byte(val), &user); err != nil {
			c.logger.Errorf("failed to json unmarshal userInfo value: %s", err)
		} else {
			return &user, nil
		}
	}

	c.metrics.CounterInc(slackClientCacheRequestsTotalMetric, action, "miss")

	f := func(ctx context.Context) (*slack.User, any, error) {
		val, err := c.api.GetUserInfoContext(ctx, userID)
		return val, nil, err
	}

	user, _, err := callAPI(ctx, c.logger, c.metrics, c.cfg, c.gate, c.sem, action, f)
	if err != nil {
		return nil, err
	}

	resultJSON, err := json.Marshal(user)
	if err != nil {
		c.logger.Errorf("failed to json marshal userInfo value: %s", err)
	} else {
		c.cache.SetWithRandomExpiration(ctx, cacheKey, string(resultJSON), time.Hour, time.Hour)
	}

	return user, nil
}

func (c *SlackAPIClient) ListUserGroupMembers(ctx context.Context, groupID string) (map[string]struct{}, error) {
	if err := c.checkConnected(); err != nil {
		return nil, err
	}

	if groupID == "" {
		return nil, errors.New("groupID cannot be empty")
	}

	action := "usergroups.users.list"

	cacheKey := "ListUserGroupMembers:" + groupID

	if val, hit := c.cache.Get(ctx, cacheKey); hit {
		c.metrics.CounterInc(slackClientCacheRequestsTotalMetric, action, "hit")

		var result map[string]struct{}

		if err := json.Unmarshal([]byte(val), &result); err != nil {
			c.logger.Errorf("failed to json unmarshal group user IDs value: %s", err)
		} else {
			return result, nil
		}
	}

	c.metrics.CounterInc(slackClientCacheRequestsTotalMetric, action, "miss")

	result := make(map[string]struct{})

	f := func(ctx context.Context) ([]string, any, error) {
		val, err := c.api.GetUserGroupMembersContext(ctx, groupID)
		return val, nil, err
	}

	userIDs, _, err := callAPI(ctx, c.logger, c.metrics, c.cfg, c.gate, c.sem, action, f, SlackNoSuchSubTeamError)
	if err != nil {
		if err.Error() == SlackNoSuchSubTeamError {
			return result, nil
		}
		return nil, err
	}

	for _, userID := range userIDs {
		result[userID] = struct{}{}
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		c.logger.Errorf("failed to json marshal user IDs: %s", err)
	} else {
		c.cache.SetWithRandomExpiration(ctx, cacheKey, string(resultJSON), time.Minute, 30*time.Second)
	}

	return result, nil
}

func (c *SlackAPIClient) GetUserIDsInChannel(ctx context.Context, channelID string) (map[string]struct{}, error) {
	if err := c.checkConnected(); err != nil {
		return nil, err
	}

	if channelID == "" {
		return nil, errors.New("channelID cannot be empty")
	}

	action := "conversations.members"

	cacheKey := "GetUserIDsInChannel:" + channelID

	if val, hit := c.cache.Get(ctx, cacheKey); hit {
		c.metrics.CounterInc(slackClientCacheRequestsTotalMetric, action, "hit")

		var result map[string]struct{}

		if err := json.Unmarshal([]byte(val), &result); err != nil {
			c.logger.WithField("channel_id", channelID).Errorf("failed to json unmarshal userIdsInChannel: %s", err)
		} else {
			return result, nil
		}
	}

	userIDs := []string{}
	result := make(map[string]struct{})

	c.metrics.CounterInc(slackClientCacheRequestsTotalMetric, action, "miss")

	params := &slack.GetUsersInConversationParameters{
		ChannelID: channelID,
	}

	f := func(ctx context.Context) ([]string, string, error) {
		return c.api.GetUsersInConversationContext(ctx, params)
	}

	for {
		users, nextCursor, err := callAPI(ctx, c.logger.WithField("channel_id", channelID), c.metrics, c.cfg, c.gate, c.sem, action, f, SlackChannelNotFoundError)
		if err != nil {
			if err.Error() == SlackChannelNotFoundError {
				return result, nil
			}
			return nil, err
		}

		userIDs = append(userIDs, users...)

		if nextCursor == "" {
			break
		}

		params.Cursor = nextCursor
	}

	for _, userID := range userIDs {
		result[userID] = struct{}{}
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		c.logger.WithField("channel_id", channelID).Errorf("failed to json marshal userIdsInChannel result: %s", err)
	} else {
		c.cache.SetWithRandomExpiration(ctx, cacheKey, string(resultJSON), time.Minute, 30*time.Second)
	}

	return result, nil
}

func (c *SlackAPIClient) BotIsInChannel(ctx context.Context, channelID string) (bool, error) {
	if err := c.checkConnected(); err != nil {
		return false, err
	}

	if channelID == "" {
		return false, errors.New("channelID cannot be empty")
	}

	cacheKey := "BotIsInChannel:" + channelID

	if val, hit := c.cache.Get(ctx, cacheKey); hit {
		return val == "true", nil
	}

	userIDs, err := c.GetUserIDsInChannel(ctx, channelID)
	if err != nil {
		return false, err
	}

	_, found := userIDs[c.botUserID]

	if found {
		c.cache.SetWithRandomExpiration(ctx, cacheKey, "true", 5*time.Minute, time.Minute)
	} else {
		c.cache.SetWithRandomExpiration(ctx, cacheKey, "false", 30*time.Second, 15*time.Second)
	}

	return found, nil
}

func (c *SlackAPIClient) checkConnected() error {
	if !c.connected {
		return ErrNotConnected
	}
	return nil
}

func callAPI[V any, W any](ctx context.Context, logger types.Logger, metrics types.Metrics,
	cfg *config.SlackClientConfig, gate rateLimitSignaler, sem *semaphore.Weighted,
	action string, f func(ctx context.Context) (V, W, error), expectedErrors ...string,
) (V, W, error) {
	attempt := 1
	started := time.Now()

	for {
		// Acquire a semaphore slot before the HTTP call to limit concurrency.
		if sem != nil {
			if err := sem.Acquire(ctx, 1); err != nil {
				var result1 V
				var result2 W
				return result1, result2, err
			}
		}

		val1, val2, err := f(ctx)

		if sem != nil {
			sem.Release(1)
		}

		metrics.CounterInc(slackAPICallsTotalMetric, action)

		logger.
			WithField("action", action).
			WithField("response_val_1", val1).
			WithField("response_val_2", val2).
			WithField("error", err).
			Debug("Slack API call response")

		if err == nil {
			return val1, val2, nil
		}

		var result1 V
		var result2 W

		if ctx.Err() != nil {
			return result1, result2, err
		}

		if len(expectedErrors) > 0 && slices.Contains(expectedErrors, err.Error()) {
			return result1, result2, err
		}

		errType := classifySlackError(err)
		metrics.CounterInc(slackAPIErrorsTotalMetric, action, errType)

		if !isRetryableError(err) {
			return result1, result2, err
		}

		if waitErr := waitForAPIError(ctx, started, logger, attempt, action, cfg, gate, metrics, err); waitErr != nil {
			return result1, result2, waitErr
		}

		metrics.CounterInc(slackAPIRetriesTotalMetric, action, errType)
		attempt++
	}
}

func waitForAPIError(ctx context.Context, started time.Time, logger types.Logger, attempt int, action string, cfg *config.SlackClientConfig, gate rateLimitSignaler, metrics types.Metrics, err error) error {
	var rateLimitError *slack.RateLimitedError

	if errors.As(err, &rateLimitError) {
		remainingWaitTime := time.Until(started.Add(time.Duration(cfg.MaxRateLimitErrorWaitTimeSeconds) * time.Second))

		if attempt >= cfg.MaxAttemptsForRateLimitError || remainingWaitTime < time.Second {
			return fmt.Errorf("failed to call Slack API %s after %d attempts and %d seconds: rate limit error: %w", action, attempt, int(time.Since(started).Seconds()), err)
		}

		metrics.Observe(slackAPIRateLimitDurationMetric, rateLimitError.RetryAfter.Seconds())

		if gate != nil {
			until := time.Now().Add(rateLimitError.RetryAfter + 2*time.Second)
			if signalErr := gate.Signal(ctx, until); signalErr != nil {
				logger.WithField("error", signalErr).WithField("action", action).Info("Failed to signal rate limit gate")
			}
		}

		return waitForRateLimit(ctx, logger, rateLimitError, attempt, action, remainingWaitTime)
	}

	if isTransientError(err) {
		remainingWaitTime := time.Until(started.Add(time.Duration(cfg.MaxTransientErrorWaitTimeSeconds) * time.Second))

		if attempt >= cfg.MaxAttemptsForTransientError || remainingWaitTime < time.Second {
			return fmt.Errorf("failed to call Slack API %s after %d attempts and %d seconds: transient error: %w", action, attempt, int(time.Since(started).Seconds()), err)
		}

		return waitForTransientError(ctx, logger, err, attempt, action, remainingWaitTime)
	}

	remainingWaitTime := time.Until(started.Add(time.Duration(cfg.MaxFatalErrorWaitTimeSeconds) * time.Second))

	if attempt >= cfg.MaxAttemptsForFatalError || remainingWaitTime < time.Second {
		return fmt.Errorf("failed to call Slack API %s after %d attempts and %d seconds: fatal error: %w", action, attempt, int(time.Since(started).Seconds()), err)
	}

	return waitForFatalError(ctx, logger, err, attempt, action, remainingWaitTime)
}

func waitForRateLimit(ctx context.Context, logger types.Logger, err *slack.RateLimitedError, attempt int, action string, remainingWaitTime time.Duration) error {
	wait := min(err.RetryAfter+2*time.Second, remainingWaitTime)

	logger.WithField("action", action).WithField("wait", wait).WithField("attempt", attempt).Info("Slack API rate limit exceeded, waiting before retry")

	return sleep(ctx, wait)
}

func waitForTransientError(ctx context.Context, logger types.Logger, err error, attempt int, action string, remainingWaitTime time.Duration) error {
	wait := min(time.Duration(attempt)*time.Second, remainingWaitTime)

	logger.WithField("error", err).WithField("action", action).WithField("wait", wait).WithField("attempt", attempt).Info("Slack transient error, waiting before retry")

	return sleep(ctx, wait)
}

func waitForFatalError(ctx context.Context, logger types.Logger, err error, attempt int, action string, remainingWaitTime time.Duration) error {
	wait := min(time.Duration(attempt)*time.Second*2, remainingWaitTime)

	logger.WithField("error", err).WithField("action", action).WithField("wait", wait).WithField("attempt", attempt).Info("Slack fatal error, waiting before retry")

	return sleep(ctx, wait)
}

func sleep(ctx context.Context, t time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(t):
		return nil
	}
}

// slackErrorMeta holds classification properties for a known Slack API error string.
type slackErrorMeta struct {
	label       string // metric label
	isTransient bool   // true → retry with transient backoff
	isRetryable bool   // false → non-retryable, bail immediately
}

//nolint:gochecknoglobals
var slackErrors = map[string]slackErrorMeta{
	// Transient — safe to retry with linear backoff
	slackInternalError:           {label: "transient", isTransient: true, isRetryable: true},
	slackFatalError:              {label: "transient", isTransient: true, isRetryable: true},
	slackServiceUnavailableError: {label: "transient", isTransient: true, isRetryable: true},
	slackRequestTimeoutError:     {label: "transient", isTransient: true, isRetryable: true},
	slackRateLimitedError:        {label: "transient", isTransient: true, isRetryable: true},
	// Non-retryable — permanent failures, labelled by error code
	SlackChannelNotFoundError:   {label: SlackChannelNotFoundError, isTransient: false, isRetryable: false},
	slackMessageNotFoundError:   {label: slackMessageNotFoundError, isTransient: false, isRetryable: false},
	slackCantUpdateMessageError: {label: slackCantUpdateMessageError, isTransient: false, isRetryable: false},
	slackCantDeleteMessageError: {label: slackCantDeleteMessageError, isTransient: false, isRetryable: false},
	slackInvalidBlocksError:     {label: slackInvalidBlocksError, isTransient: false, isRetryable: false},
	slackNotInChannelError:      {label: slackNotInChannelError, isTransient: false, isRetryable: false},
	slackIsArchivedError:        {label: slackIsArchivedError, isTransient: false, isRetryable: false},
	slackRestrictedActionError:  {label: slackRestrictedActionError, isTransient: false, isRetryable: false},
}

// classifyError is the single classification function for all Slack API errors.
// All other error-classification helpers delegate to it.
func classifyError(err error) slackErrorMeta {
	if err == nil {
		return slackErrorMeta{label: "unknown", isTransient: false, isRetryable: true}
	}

	// Rate limit check must come first: *slack.RateLimitedError also matches
	// "ratelimited" in the map, but gets its own dedicated retry path.
	var rateLimitErr *slack.RateLimitedError
	if errors.As(err, &rateLimitErr) {
		return slackErrorMeta{label: "rate_limited", isTransient: false, isRetryable: true}
	}

	// HTTP client timeout — distinct from the caller's context expiring (ctx.Err() != nil).
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return slackErrorMeta{label: "transient", isTransient: true, isRetryable: true}
	}

	// If the error type implements a Retryable() method, use it to determine retryability.
	if v, ok := any(err).(retryable); ok && v.Retryable() {
		return slackErrorMeta{label: "transient", isTransient: true, isRetryable: true}
	}

	// Check the error string against known Slack API error codes to classify it for metrics and retry logic.
	if meta, ok := slackErrors[err.Error()]; ok {
		return meta
	}

	// Unrecognized error — assume it's a transient issue worth retrying, but label it "unknown" for,
	// metrics so we can investigate and add it to the map if needed.
	return slackErrorMeta{label: "unknown", isTransient: false, isRetryable: true}
}

// classifySlackError returns a bounded metric label for a Slack API error.
func classifySlackError(err error) string {
	if err == nil {
		return "unknown"
	}

	return classifyError(err).label
}

func isTransientError(err error) bool {
	return classifyError(err).isTransient
}

func isRetryableError(err error) bool {
	return classifyError(err).isRetryable
}
