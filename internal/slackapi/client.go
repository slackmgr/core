package slackapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/eko/gocache/lib/v4/cache"
	cachestore "github.com/eko/gocache/lib/v4/store"
	commonlib "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/peteraglen/slack-manager/internal"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

const (
	slackRequestMetric   = "slack_request"
	slackhitMetric       = "slack_cache_hit"
	slackAPICallMetric   = "slack_api_call"
	slackAPIErrorMetric  = "slack_api_error"
	ChannelNotFoundError = "channel_not_found"
)

type retryable interface{ Retryable() bool }

type Client struct {
	api         *slack.Client
	logger      commonlib.Logger
	cache       *internal.Cache[string]
	cachePrefix string
	metrics     commonlib.Metrics
	cfg         *config.SlackClientConfig
	connected   bool
	botUserID   string
}

func New(cacheStore cachestore.StoreInterface, cachePrefix string, logger commonlib.Logger, metrics commonlib.Metrics, cfg *config.SlackClientConfig) *Client {
	cfg.SetDefaults()

	cache := internal.NewCache(cache.New[string](cacheStore), logger)

	if cachePrefix == "" {
		cachePrefix = "slack-manager"
	}

	return &Client{
		logger:      logger,
		cache:       cache,
		cachePrefix: fmt.Sprintf("%s::slackapi::", cachePrefix),
		metrics:     metrics,
		cfg:         cfg,
	}
}

func (c *Client) Connect(ctx context.Context) (*slack.AuthTestResponse, error) {
	if c.connected {
		return nil, fmt.Errorf("connect can only be run once")
	}

	if c.metrics == nil {
		return nil, fmt.Errorf("client metrics cannot be nil")
	}

	if c.cache == nil {
		return nil, fmt.Errorf("client cache cannot be nil")
	}

	if c.cfg == nil {
		return nil, fmt.Errorf("client options cannot be nil")
	}

	if c.cfg.BotToken == "" {
		return nil, fmt.Errorf("client bot token cannot be nil")
	}

	c.metrics.RegisterCounter(slackRequestMetric, "Total number of Slack client requests", "slack_action")
	c.metrics.RegisterCounter(slackhitMetric, "Total number of Slack client cache hits", "slack_action")
	c.metrics.RegisterCounter(slackAPICallMetric, "Total number of Slack API calls", "slack_action")
	c.metrics.RegisterCounter(slackAPIErrorMetric, "Total number of Slack API call errors", "slack_action")

	httpClient := &http.Client{
		Timeout: c.cfg.HTTPTimeout,
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
		return nil, fmt.Errorf("missing bot user ID in auth test response")
	}

	c.botUserID = response.UserID

	c.logger.Infof("Slack authorization tested OK with bot user %s (%s)", response.User, response.UserID)

	c.connected = true

	return response, nil
}

func (c *Client) API() *slack.Client {
	return c.api
}

func (c *Client) NewSocketModeClient() *socketmode.Client {
	return socketmode.New(c.api, socketmode.OptionDebug(c.cfg.DebugLogging), socketmode.OptionLog(&slackApilogger{logger: c.logger}))
}

func (c *Client) BotUserID() string {
	return c.botUserID
}

func (c *Client) ChatPostMessage(ctx context.Context, channelID string, options ...slack.MsgOption) (string, error) {
	action := "chat.post"

	c.metrics.AddToCounter(slackRequestMetric, 1, action)

	f := func(ctx context.Context) (string, any, error) {
		_, ts, _, err := c.api.SendMessageContext(ctx, channelID, options...)
		return ts, nil, err
	}

	ts, _, err := callAPI(ctx, c.logger.WithField("slack_channel_id", channelID), c.metrics, c.cfg, action, f)
	if err != nil {
		return "", err
	}

	return ts, nil
}

func (c *Client) ChatUpdateMessage(ctx context.Context, channelID string, options ...slack.MsgOption) (string, error) {
	action := "chat.update"

	c.metrics.AddToCounter(slackRequestMetric, 1, action)

	f := func(ctx context.Context) (string, any, error) {
		_, ts, _, err := c.api.SendMessageContext(ctx, channelID, options...)
		return ts, nil, err
	}

	ts, _, err := callAPI(ctx, c.logger.WithField("slack_channel_id", channelID), c.metrics, c.cfg, action, f)
	if err != nil {
		return "", err
	}

	return ts, nil
}

func (c *Client) ChatDeleteMessage(ctx context.Context, channelID string, ts string) error {
	action := "chat.delete"

	c.metrics.AddToCounter(slackRequestMetric, 1, action)

	f := func(ctx context.Context) (any, any, error) {
		_, _, _, err := c.api.SendMessageContext(ctx, channelID, slack.MsgOptionDelete(ts))
		return nil, nil, err
	}

	if _, _, err := callAPI(ctx, c.logger.WithField("slack_channel_id", channelID), c.metrics, c.cfg, action, f); err != nil {
		return err
	}

	return nil
}

func (c *Client) SendResponse(ctx context.Context, channelID, responseURL, responseType, text string) error {
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

	c.metrics.AddToCounter(slackRequestMetric, 1, action)

	options := []slack.MsgOption{slack.MsgOptionResponseURL(responseURL, responseType), slack.MsgOptionText(text, false)}

	f := func(ctx context.Context) (any, any, error) {
		_, _, _, err := c.api.SendMessageContext(ctx, channelID, options...)
		return nil, nil, err
	}

	if _, _, err := callAPI(ctx, c.logger.WithField("slack_channel_id", channelID), c.metrics, c.cfg, action, f); err != nil {
		return fmt.Errorf("failed to send Slack response in channel %s: %w", channelID, err)
	}

	return nil
}

func (c *Client) PostEphemeral(ctx context.Context, channelID, userID string, options ...slack.MsgOption) (string, error) {
	if channelID == "" {
		return "", errors.New("channelID cannot be empty")
	}

	if userID == "" {
		return "", errors.New("userID cannot be empty")
	}

	action := "chat.postEphemeral"

	c.metrics.AddToCounter(slackRequestMetric, 1, action)

	f := func(ctx context.Context) (string, any, error) {
		ts, err := c.api.PostEphemeralContext(ctx, channelID, userID, options...)
		return ts, nil, err
	}

	ts, _, err := callAPI(ctx, c.logger.WithField("slack_channel_id", channelID), c.metrics, c.cfg, action, f)
	if err != nil {
		return "", fmt.Errorf("failed to post ephemeral Slack message to user %s in channel %s: %w", userID, channelID, err)
	}

	return ts, nil
}

func (c *Client) OpenModal(ctx context.Context, triggerID string, request slack.ModalViewRequest) error {
	if triggerID == "" {
		return errors.New("triggerID cannot be empty")
	}

	action := "views.open"

	c.metrics.AddToCounter(slackRequestMetric, 1, action)

	_, err := c.api.OpenViewContext(ctx, triggerID, request)
	if err != nil {
		return fmt.Errorf("failed to open modal Slack view: %w", err)
	}

	return nil
}

func (c *Client) MessageHasReplies(ctx context.Context, channelID, ts string) (bool, error) {
	if channelID == "" {
		return false, errors.New("channelID cannot be empty")
	}

	if ts == "" {
		return false, errors.New("ts cannot be empty")
	}

	action := "conversations.replies"

	c.metrics.AddToCounter(slackRequestMetric, 1, action)

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

	msgs, _, err := callAPI(ctx, c.logger.WithField("slack_channel_id", channelID), c.metrics, c.cfg, action, f, ChannelNotFoundError, "thread_not_found")
	if err != nil {
		if err.Error() == ChannelNotFoundError || err.Error() == "thread_not_found" {
			return false, nil
		}
		return false, fmt.Errorf("failed to read Slack message %s replies in channel %s: %w", ts, channelID, err)
	}

	return len(msgs) > 1, nil
}

func (c *Client) GetChannelInfo(ctx context.Context, channelID string) (*slack.Channel, error) {
	if channelID == "" {
		return nil, errors.New("channelID cannot be empty")
	}

	action := "conversations.info"

	c.metrics.AddToCounter(slackRequestMetric, 1, action)

	cacheKey := fmt.Sprintf("%s::GetChannelInfo::%s", c.cachePrefix, channelID)

	if val, hit := c.cache.Get(ctx, cacheKey); hit {
		c.metrics.AddToCounter(slackhitMetric, 1, action)

		info := slack.Channel{}

		if err := json.Unmarshal([]byte(val), &info); err != nil {
			c.logger.WithField("slack_channel_id", channelID).Errorf("failed to json unmarshal channel value: %s", err)
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

	channel, _, err := callAPI(ctx, c.logger.WithField("slack_channel_id", channelID), c.metrics, c.cfg, action, f, ChannelNotFoundError)
	if err != nil {
		if err.Error() == ChannelNotFoundError {
			channel = &slack.Channel{
				GroupConversation: slack.GroupConversation{
					Conversation: slack.Conversation{
						ID: channelID,
					},
					Name: "UNKNOWN_CHANNEL",
				},
			}
		} else {
			return nil, fmt.Errorf("failed to read Slack channel info for %s: %w", channelID, err)
		}
	}

	resultJSON, err := json.Marshal(channel)
	if err != nil {
		c.logger.WithField("slack_channel_id", channelID).Errorf("failed to json marshal channel value: %w", err)
	} else {
		c.cache.SetWithRandomExpiration(ctx, cacheKey, string(resultJSON), 10*time.Second, 10*time.Second)
	}

	return channel, nil
}

func (c *Client) ListBotChannels(ctx context.Context) ([]*internal.ChannelSummary, error) {
	action := "users.conversations"

	c.metrics.AddToCounter(slackRequestMetric, 1, action)

	cacheKey := fmt.Sprintf("%s::ListBotChannels", c.cachePrefix)

	if val, hit := c.cache.Get(ctx, cacheKey); hit {
		c.metrics.AddToCounter(slackhitMetric, 1, action)

		channels := []*internal.ChannelSummary{}

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

	channels := []*internal.ChannelSummary{}

	for {
		chs, nextCursor, err := callAPI(ctx, c.logger, c.metrics, c.cfg, action, f)
		if err != nil {
			return nil, err
		}

		for _, ch := range chs {
			channels = append(channels, internal.NewChannelSummary(ch))
		}

		if nextCursor == "" {
			break
		}

		params.Cursor = nextCursor
	}

	slices.SortFunc(channels, func(a, b *internal.ChannelSummary) int {
		return strings.Compare(a.Name, b.Name)
	})

	resultJSON, err := json.Marshal(channels)
	if err != nil {
		c.logger.Errorf("failed to json marshal bot channel list: %w", err)
	} else {
		c.cache.Set(ctx, cacheKey, string(resultJSON), time.Minute)
	}

	return channels, nil
}

func (c *Client) GetUserInfo(ctx context.Context, userID string) (*slack.User, error) {
	if userID == "" {
		return nil, errors.New("userID cannot be empty")
	}

	action := "users.info"

	c.metrics.AddToCounter(slackRequestMetric, 1, action)

	cacheKey := fmt.Sprintf("%s::GetUserInfo::%s", c.cachePrefix, userID)

	if val, hit := c.cache.Get(ctx, cacheKey); hit {
		c.metrics.AddToCounter(slackhitMetric, 1, action)

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
		return nil, fmt.Errorf("failed to read Slack user info for %s: %w", userID, err)
	}

	resultJSON, err := json.Marshal(user)
	if err != nil {
		c.logger.Errorf("failed to json marshal userInfo value: %s", err)
	} else {
		c.cache.SetWithRandomExpiration(ctx, cacheKey, string(resultJSON), time.Hour, time.Hour)
	}

	return user, nil
}

func (c *Client) ListUserGroupMembers(ctx context.Context, groupID string) (map[string]struct{}, error) {
	if groupID == "" {
		return nil, errors.New("groupID cannot be empty")
	}

	action := "usergroups.users.list"

	c.metrics.AddToCounter(slackRequestMetric, 1, action)

	cacheKey := fmt.Sprintf("%s::ListUserGroupMembers::%s", c.cachePrefix, groupID)

	if val, hit := c.cache.Get(ctx, cacheKey); hit {
		c.metrics.AddToCounter(slackhitMetric, 1, action)

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

	userIDs, _, err := callAPI(ctx, c.logger, c.metrics, c.cfg, action, f, "no_such_subteam")
	if err != nil {
		if err.Error() == "no_such_subteam" {
			return result, nil
		}
		return nil, fmt.Errorf("failed to list Slack user group members for %s: %w", groupID, err)
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

func (c *Client) GetUserIDsInChannel(ctx context.Context, channelID string) (map[string]struct{}, error) {
	if channelID == "" {
		return nil, errors.New("channelID cannot be empty")
	}

	action := "conversations.members"

	c.metrics.AddToCounter(slackRequestMetric, 1, action)

	cacheKey := fmt.Sprintf("%s::GetUserIDsInChannel::%s", c.cachePrefix, channelID)

	if val, hit := c.cache.Get(ctx, cacheKey); hit {
		c.metrics.AddToCounter(slackhitMetric, 1, action)

		var result map[string]struct{}

		if err := json.Unmarshal([]byte(val), &result); err != nil {
			c.logger.WithField("slack_channel_id", channelID).Errorf("failed to json unmarshal userIdsInChannel value: %s", err)
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
		users, nextCursor, err := callAPI(ctx, c.logger.WithField("slack_channel_id", channelID), c.metrics, c.cfg, action, f, ChannelNotFoundError)
		if err != nil {
			if err.Error() == ChannelNotFoundError {
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
		c.logger.WithField("slack_channel_id", channelID).Errorf("failed to json marshal userIdsInChannel result: %s", err)
	} else {
		c.cache.SetWithRandomExpiration(ctx, cacheKey, string(resultJSON), time.Minute, 30*time.Second)
	}

	return result, nil
}

func (c *Client) BotIsInChannel(ctx context.Context, channelID string) (bool, error) {
	if channelID == "" {
		return false, errors.New("channelID cannot be empty")
	}

	cacheKey := fmt.Sprintf("%s::BotIsInChannel::%s", c.cachePrefix, channelID)

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

func callAPI[V any, W any](ctx context.Context, logger commonlib.Logger, metrics commonlib.Metrics, cfg *config.SlackClientConfig, action string, f func(ctx context.Context) (V, W, error), expectedErrors ...string) (V, W, error) {
	attempt := 1
	started := time.Now()

	for {
		val1, val2, err := f(ctx)

		metrics.AddToCounter(slackAPICallMetric, 1, action)

		logger.Debugf("Slack %s response [val1:%v val2:%v err:%v]", action, val1, val2, err)

		if err == nil {
			return val1, val2, nil
		}

		var result1 V
		var result2 W

		if internal.IsCtxCanceledErr(err) {
			return result1, result2, err
		}

		if len(expectedErrors) > 0 && inSlice(err.Error(), expectedErrors) {
			return result1, result2, err
		}

		metrics.AddToCounter(slackAPIErrorMetric, 1, action)

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
		remainingWaitTime := time.Until(started.Add(cfg.MaxRateLimitErrorWaitTime))

		if attempt >= cfg.MaxAttemtsForRateLimitError || remainingWaitTime < time.Second {
			return fmt.Errorf("failed to call Slack API %s after %d attempts and %d seconds: rate limit error: %w", action, attempt, int(time.Since(started).Seconds()), err)
		}

		return waitForRateLimit(ctx, logger, rateLimitError, attempt, action, remainingWaitTime)
	}

	if isTransientError(err) {
		remainingWaitTime := time.Until(started.Add(cfg.MaxTransientErrorWaitTime))

		if attempt >= cfg.MaxAttemptsForTransientError || remainingWaitTime < time.Second {
			return fmt.Errorf("failed to call Slack API %s after %d attempts and %d seconds: transient error: %w", action, attempt, int(time.Since(started).Seconds()), err)
		}

		return waitForTransientError(ctx, logger, err, attempt, action, remainingWaitTime)
	}

	remainingWaitTime := time.Until(started.Add(cfg.MaxFatalErrorWaitTime))

	if attempt >= cfg.MaxAttemptsForFatalError || remainingWaitTime < time.Second {
		return fmt.Errorf("failed to call Slack API %s after %d attempts and %d seconds: fatal error: %w", action, attempt, int(time.Since(started).Seconds()), err)
	}

	return waitForFatalError(ctx, logger, err, attempt, action, remainingWaitTime)
}

func inSlice(s string, slice []string) bool {
	for _, item := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func waitForRateLimit(ctx context.Context, logger commonlib.Logger, err *slack.RateLimitedError, attempt int, action string, remainingWaitTime time.Duration) error {
	wait := err.RetryAfter + 2*time.Second

	if wait > remainingWaitTime {
		wait = remainingWaitTime
	}

	logger.Infof("Slack API rate limit exceeded when calling %s - waiting %v before trying again (attempt %d)", action, wait, attempt)

	return sleep(ctx, wait)
}

func waitForTransientError(ctx context.Context, logger commonlib.Logger, err error, attempt int, action string, remainingWaitTime time.Duration) error {
	wait := time.Duration(attempt) * time.Second

	if wait > remainingWaitTime {
		wait = remainingWaitTime
	}

	logger.Infof("Slack transient error %s when calling %s - waiting %v before trying again (attempt %d)", err, action, wait, attempt)

	return sleep(ctx, wait)
}

func waitForFatalError(ctx context.Context, logger commonlib.Logger, err error, attempt int, action string, remainingWaitTime time.Duration) error {
	wait := time.Duration(attempt) * time.Second * 2

	if wait > remainingWaitTime {
		wait = remainingWaitTime
	}

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
	if v, ok := interface{}(err).(retryable); ok {
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
	case ChannelNotFoundError, "message_not_found", "cant_update_message", "cant_delete_message", "invalid_blocks", "not_in_channel", "is_archived":
		return true
	default:
		return false
	}
}
