package internal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	cachestore "github.com/eko/gocache/lib/v4/store"
	commonlib "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

const (
	// SlackChannelNotFoundError is the error string returned by the Slack API when a channel is not found.
	SlackChannelNotFoundError = "channel_not_found"

	// SlackThreadNotFoundError is the error string returned by the Slack API when a thread is not found.
	SlackThreadNotFoundError = "thread_not_found"

	// SlackNoSuchSubTeamError is the error string returned by the Slack API when a user group (sub team) is not found.
	SlackNoSuchSubTeamError = "no_such_subteam"

	slackRequestMetric  = "slack_request"
	slackCacheHitMetric = "slack_cache_hit"
	slackAPICallMetric  = "slack_api_call"
	slackAPIErrorMetric = "slack_api_error"
)

// ErrNotConnected is returned when an API method is called before Connect().
var ErrNotConnected = errors.New("client not connected: Connect() must be called first")

type retryable interface{ Retryable() bool }

type SlackAPIClient struct {
	api       *slack.Client
	logger    commonlib.Logger
	cache     *Cache
	metrics   commonlib.Metrics
	cfg       *config.SlackClientConfig
	connected bool
	botUserID string
}

func NewSlackAPIClient(cacheStore cachestore.StoreInterface, cacheKeyPrefix string, logger commonlib.Logger, metrics commonlib.Metrics, cfg *config.SlackClientConfig) *SlackAPIClient {
	cfg.SetDefaults()

	cacheKeyPrefix += "slack-api-client:"
	cache := NewCache(cacheStore, cacheKeyPrefix, logger)

	return &SlackAPIClient{
		logger:  logger,
		cache:   cache,
		metrics: metrics,
		cfg:     cfg,
	}
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

	c.metrics.RegisterCounter(slackRequestMetric, "Total number of Slack client requests", "slack_action")
	c.metrics.RegisterCounter(slackCacheHitMetric, "Total number of Slack client cache hits", "slack_action")
	c.metrics.RegisterCounter(slackAPICallMetric, "Total number of Slack API calls", "slack_action")
	c.metrics.RegisterCounter(slackAPIErrorMetric, "Total number of Slack API call errors", "slack_action")

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
	return NewSocketModeClientWrapper(s)
}

func (c *SlackAPIClient) BotUserID() string {
	return c.botUserID
}

func (c *SlackAPIClient) ChatPostMessage(ctx context.Context, channelID string, options ...slack.MsgOption) (string, error) {
	if err := c.checkConnected(); err != nil {
		return "", err
	}

	action := "chat.post"

	c.metrics.Inc(slackRequestMetric, action)

	f := func(ctx context.Context) (string, any, error) {
		_, ts, _, err := c.api.SendMessageContext(ctx, channelID, options...)
		return ts, nil, err
	}

	ts, _, err := callAPI(ctx, c.logger.WithField("channel_id", channelID), c.metrics, c.cfg, action, f)

	return ts, err
}

func (c *SlackAPIClient) ChatUpdateMessage(ctx context.Context, channelID string, options ...slack.MsgOption) (string, error) {
	if err := c.checkConnected(); err != nil {
		return "", err
	}

	action := "chat.update"

	c.metrics.Inc(slackRequestMetric, action)

	f := func(ctx context.Context) (string, any, error) {
		_, ts, _, err := c.api.SendMessageContext(ctx, channelID, options...)
		return ts, nil, err
	}

	ts, _, err := callAPI(ctx, c.logger.WithField("channel_id", channelID), c.metrics, c.cfg, action, f)

	return ts, err
}

func (c *SlackAPIClient) ChatDeleteMessage(ctx context.Context, channelID string, ts string) error {
	if err := c.checkConnected(); err != nil {
		return err
	}

	action := "chat.delete"

	c.metrics.Inc(slackRequestMetric, action)

	f := func(ctx context.Context) (any, any, error) {
		_, _, _, err := c.api.SendMessageContext(ctx, channelID, slack.MsgOptionDelete(ts))
		return nil, nil, err
	}

	_, _, err := callAPI(ctx, c.logger.WithField("channel_id", channelID), c.metrics, c.cfg, action, f)

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

	c.metrics.Inc(slackRequestMetric, action)

	options := []slack.MsgOption{slack.MsgOptionResponseURL(responseURL, responseType), slack.MsgOptionText(text, false)}

	f := func(ctx context.Context) (any, any, error) {
		_, _, _, err := c.api.SendMessageContext(ctx, channelID, options...)
		return nil, nil, err
	}

	_, _, err := callAPI(ctx, c.logger.WithField("channel_id", channelID), c.metrics, c.cfg, action, f)

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

	c.metrics.Inc(slackRequestMetric, action)

	f := func(ctx context.Context) (string, any, error) {
		ts, err := c.api.PostEphemeralContext(ctx, channelID, userID, options...)
		return ts, nil, err
	}

	ts, _, err := callAPI(ctx, c.logger.WithField("channel_id", channelID), c.metrics, c.cfg, action, f)

	return ts, err
}

func (c *SlackAPIClient) OpenModal(ctx context.Context, triggerID string, request slack.ModalViewRequest) error {
	if err := c.checkConnected(); err != nil {
		return err
	}

	if triggerID == "" {
		return errors.New("triggerID cannot be empty")
	}

	action := "views.open"

	c.metrics.Inc(slackRequestMetric, action)

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

	c.metrics.Inc(slackRequestMetric, action)

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

	msgs, _, err := callAPI(ctx, c.logger.WithField("channel_id", channelID), c.metrics, c.cfg, action, f, SlackChannelNotFoundError, SlackThreadNotFoundError)
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

	c.metrics.Inc(slackRequestMetric, action)

	cacheKey := "GetChannelInfo:" + channelID

	if val, hit := c.cache.Get(ctx, cacheKey); hit {
		c.metrics.Inc(slackCacheHitMetric, action)

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

	f := func(ctx context.Context) (*slack.Channel, any, error) {
		input := slack.GetConversationInfoInput{
			ChannelID:     channelID,
			IncludeLocale: false,
		}
		val, err := c.api.GetConversationInfoContext(ctx, &input)
		return val, nil, err
	}

	channel, _, err := callAPI(ctx, c.logger.WithField("channel_id", channelID), c.metrics, c.cfg, action, f, SlackChannelNotFoundError)
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

	c.metrics.Inc(slackRequestMetric, action)

	cacheKey := "ListBotChannels"

	if val, hit := c.cache.Get(ctx, cacheKey); hit {
		c.metrics.Inc(slackCacheHitMetric, action)

		channels := []*ChannelSummary{}

		if err := json.Unmarshal([]byte(val), &channels); err != nil {
			c.logger.Errorf("failed to json unmarshal channel list: %s", err)
		} else {
			return channels, nil
		}
	}

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
		chs, nextCursor, err := callAPI(ctx, c.logger, c.metrics, c.cfg, action, f)
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

	c.metrics.Inc(slackRequestMetric, action)

	cacheKey := "GetUserInfo:" + userID

	if val, hit := c.cache.Get(ctx, cacheKey); hit {
		c.metrics.Inc(slackCacheHitMetric, action)

		user := slack.User{}

		if err := json.Unmarshal([]byte(val), &user); err != nil {
			c.logger.Errorf("failed to json unmarshal userInfo value: %s", err)
		} else {
			return &user, nil
		}
	}

	f := func(ctx context.Context) (*slack.User, any, error) {
		val, err := c.api.GetUserInfoContext(ctx, userID)
		return val, nil, err
	}

	user, _, err := callAPI(ctx, c.logger, c.metrics, c.cfg, action, f)
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

	c.metrics.Inc(slackRequestMetric, action)

	cacheKey := "ListUserGroupMembers:" + groupID

	if val, hit := c.cache.Get(ctx, cacheKey); hit {
		c.metrics.Inc(slackCacheHitMetric, action)

		var result map[string]struct{}

		if err := json.Unmarshal([]byte(val), &result); err != nil {
			c.logger.Errorf("failed to json unmarshal group user IDs value: %s", err)
		} else {
			return result, nil
		}
	}

	result := make(map[string]struct{})

	f := func(ctx context.Context) ([]string, any, error) {
		val, err := c.api.GetUserGroupMembersContext(ctx, groupID)
		return val, nil, err
	}

	userIDs, _, err := callAPI(ctx, c.logger, c.metrics, c.cfg, action, f, SlackNoSuchSubTeamError)
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

	c.metrics.Inc(slackRequestMetric, action)

	cacheKey := "GetUserIDsInChannel:" + channelID

	if val, hit := c.cache.Get(ctx, cacheKey); hit {
		c.metrics.Inc(slackCacheHitMetric, action)

		var result map[string]struct{}

		if err := json.Unmarshal([]byte(val), &result); err != nil {
			c.logger.WithField("channel_id", channelID).Errorf("failed to json unmarshal userIdsInChannel: %s", err)
		} else {
			return result, nil
		}
	}

	userIDs := []string{}
	result := make(map[string]struct{})

	params := &slack.GetUsersInConversationParameters{
		ChannelID: channelID,
	}

	f := func(ctx context.Context) ([]string, string, error) {
		return c.api.GetUsersInConversationContext(ctx, params)
	}

	for {
		users, nextCursor, err := callAPI(ctx, c.logger.WithField("channel_id", channelID), c.metrics, c.cfg, action, f, SlackChannelNotFoundError)
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

func callAPI[V any, W any](ctx context.Context, logger commonlib.Logger, metrics commonlib.Metrics,
	cfg *config.SlackClientConfig, action string, f func(ctx context.Context) (V, W, error), expectedErrors ...string,
) (V, W, error) {
	attempt := 1
	started := time.Now()

	for {
		val1, val2, err := f(ctx)

		metrics.Inc(slackAPICallMetric, action)

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

		if IsCtxCanceledErr(err) {
			return result1, result2, err
		}

		if len(expectedErrors) > 0 && slices.Contains(expectedErrors, err.Error()) {
			return result1, result2, err
		}

		metrics.Inc(slackAPIErrorMetric, action)

		if isNonRetryableError(err) {
			return result1, result2, err
		}

		if waitErr := waitForAPIError(ctx, started, logger, attempt, action, cfg, err); waitErr != nil {
			return result1, result2, waitErr
		}

		attempt++
	}
}

func waitForAPIError(ctx context.Context, started time.Time, logger commonlib.Logger, attempt int, action string, cfg *config.SlackClientConfig, err error) error {
	var rateLimitError *slack.RateLimitedError

	if errors.As(err, &rateLimitError) {
		remainingWaitTime := time.Until(started.Add(time.Duration(cfg.MaxRateLimitErrorWaitTimeSeconds) * time.Second))

		if attempt >= cfg.MaxAttemptsForRateLimitError || remainingWaitTime < time.Second {
			return fmt.Errorf("failed to call Slack API %s after %d attempts and %d seconds: rate limit error: %w", action, attempt, int(time.Since(started).Seconds()), err)
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

func waitForRateLimit(ctx context.Context, logger commonlib.Logger, err *slack.RateLimitedError, attempt int, action string, remainingWaitTime time.Duration) error {
	wait := min(err.RetryAfter+2*time.Second, remainingWaitTime)

	logger.Infof("Slack API rate limit exceeded when calling %s - waiting %v before trying again (attempt %d)", action, wait, attempt)

	return sleep(ctx, wait)
}

func waitForTransientError(ctx context.Context, logger commonlib.Logger, err error, attempt int, action string, remainingWaitTime time.Duration) error {
	wait := min(time.Duration(attempt)*time.Second, remainingWaitTime)

	logger.Infof("Slack transient error %s when calling %s - waiting %v before trying again (attempt %d)", err, action, wait, attempt)

	return sleep(ctx, wait)
}

func waitForFatalError(ctx context.Context, logger commonlib.Logger, err error, attempt int, action string, remainingWaitTime time.Duration) error {
	wait := min(time.Duration(attempt)*time.Second*2, remainingWaitTime)

	logger.Infof("Slack fatal error %s when calling %s - waiting %v before trying again (attempt %d)", err, action, wait, attempt)

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

func isTransientError(err error) bool {
	if v, ok := any(err).(retryable); ok {
		return v.Retryable()
	}

	switch err.Error() {
	case "internal_error", "fatal_error", "service_unavailable", "request_timeout", "ratelimited":
		return true
	default:
		return false
	}
}

func isNonRetryableError(err error) bool {
	if err == nil {
		return false
	}

	switch err.Error() {
	case SlackChannelNotFoundError, "message_not_found", "cant_update_message", "cant_delete_message", "invalid_blocks", "not_in_channel", "is_archived":
		return true
	default:
		return false
	}
}
