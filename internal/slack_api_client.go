package internal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
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
	// slackClientCacheRequestsTotalMetric counts calls to the cacheable Slack client wrapper
	// methods, labelled by result ("hit" = served from cache, "miss" = required an API call).
	slackClientCacheRequestsTotalMetric = "slack_client_cache_requests_total"

	// slackAPIErrorsTotalMetric counts Slack API call errors, labelled by error_type.
	slackAPIErrorsTotalMetric = "slack_api_errors_total"

	// slackAPIRetriesTotalMetric counts retry attempts after a recoverable Slack API error.
	slackAPIRetriesTotalMetric = "slack_api_retries_total"

	// slackAPIRateLimitDurationMetric is a histogram of Retry-After durations from Slack
	// 429 responses — how many seconds the server asked us to back off — labelled by the API action.
	slackAPIRateLimitDurationMetric = "slack_api_rate_limit_duration_seconds"

	// slackAPICallDurationMetric is a histogram of individual outbound Slack HTTP call
	// durations in seconds, labelled by the API action.
	slackAPICallDurationMetric = "slack_api_call_duration_seconds"
)

// ErrNotConnected is returned when an API method is called before Connect().
var ErrNotConnected = errors.New("client not connected: Connect() must be called first")

// ErrPostOutcomeUnknown is returned (wrapping the underlying error) by [SlackAPIClient.ChatPostMessage]
// when the call failed in a way where the message may still have been posted, e.g. a timeout or a
// Slack fatal_error. Such errors are not retried, since a retry could create a duplicate message.
var ErrPostOutcomeUnknown = errors.New("outcome of Slack post is unknown")

// ErrTooManyMessages is returned (wrapped) by [SlackAPIClient.ListBotMessages] when the channel has too many messages
// since oldest to read within [maxBotMessagesPages] pages. Unlike other errors, it depends on how far back the messages
// are read, so a read from a later oldest time may still succeed.
var ErrTooManyMessages = errors.New("too many messages in channel")

// maxBotMessagesPages is the maximum number of conversations.history pages read by [SlackAPIClient.ListBotMessages].
const maxBotMessagesPages = 5

// How long cached lookups are kept after they are no longer fresh, to be served while the lookup method is rate
// limited. Channel and user info change rarely, so a day-old value is better than an error. Memberships are used for
// permission checks, so they are served stale for a shorter time.
const (
	channelInfoMaxStale = 24 * time.Hour
	userInfoMaxStale    = 24 * time.Hour
	membershipMaxStale  = time.Hour
)

// rateLimitSignaler is satisfied by any type that can record a rate-limit
// deadline so that all callers can back off until it expires. It is only
// signalled for 429s on write methods (see [RateLimitGroupWrite]).
type rateLimitSignaler interface {
	Signal(ctx context.Context, until time.Time) error
}

type SlackAPIClient struct {
	api       *slack.Client
	logger    types.Logger
	cache     *Cache
	metrics   types.Metrics
	cfg       *config.SlackClientConfig
	gate      rateLimitSignaler   // nil = no global gate signalling (write methods only)
	backoff   *methodBackoff      // per-method rate limit deadlines; nil = disabled
	apiURL    string              // Slack API base URL override (tests only); empty = default
	sem       *semaphore.Weighted // limits concurrent Slack API calls
	connected bool
	botUserID string
	botID     string
}

// WithRateLimitGate is a functional option that wires a global rate limit gate into
// the SlackAPIClient, so that a 429 on a write method (see [RateLimitGroupWrite]) is
// broadcast to all callers. 429s on lookup methods only back off the method itself.
func WithRateLimitGate(g rateLimitSignaler) func(*SlackAPIClient) {
	return func(c *SlackAPIClient) { c.gate = g }
}

// WithAPIURL is a functional option that overrides the Slack API base URL (which must end with a slash).
// It is intended for tests that run against a fake Slack API.
func WithAPIURL(apiURL string) func(*SlackAPIClient) {
	return func(c *SlackAPIClient) { c.apiURL = apiURL }
}

// NewSlackAPIClient creates a [SlackAPIClient]. Lookups are cached in cacheStore under cacheKeyPrefix, and
// per-method rate limit deadlines are stored there too (with the key <cacheKeyPrefix>rate-limit-gate:<method>), so
// that instances sharing the cache store and key prefix also share the cache and the rate limit backoffs.
func NewSlackAPIClient(cacheStore cachestore.StoreInterface, cacheKeyPrefix string, logger types.Logger, metrics types.Metrics, cfg *config.SlackClientConfig, opts ...func(*SlackAPIClient)) *SlackAPIClient {
	cfg.SetDefaults()

	cache := NewCache(cacheStore, cacheKeyPrefix+"slack-api-client:", logger)

	c := &SlackAPIClient{
		logger:  logger,
		cache:   cache,
		metrics: metrics,
		cfg:     cfg,
		backoff: newMethodBackoff(cacheStore, cacheKeyPrefix, logger),
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

	for _, action := range []string{"conversations.info", "users.conversations", "users.info", "usergroups.users.list", "conversations.members"} {
		for _, result := range []string{"hit", "miss", "stale"} {
			c.metrics.CounterAdd(slackClientCacheRequestsTotalMetric, 0, action, result)
		}
	}
	c.metrics.RegisterCounter(slackAPIErrorsTotalMetric, "Total Slack API call errors by error type", "slack_action", "error_type")
	c.metrics.RegisterCounter(slackAPIRetriesTotalMetric, "Total Slack API retry attempts after a recoverable error", "slack_action", "error_type")
	c.metrics.RegisterHistogram(slackAPIRateLimitDurationMetric, "Retry-After duration in seconds from Slack 429 responses", []float64{1, 5, 10, 30, 60, 120, 300}, "slack_action")
	c.metrics.RegisterCounter(slackAPIRateLimitedTotalMetric, "Total Slack 429 responses by action and rate limit group (write = pauses all channels, other = backs off the method only)", "slack_action", "rate_limit_group")
	c.metrics.RegisterCounter(slackAPIBackoffTotalMetric, "Total Slack API calls that found their method backing off after a 429, by outcome (waited or rejected)", "slack_action", "outcome")
	c.metrics.RegisterHistogram(slackAPICallDurationMetric, "Duration of individual outbound Slack API calls in seconds", []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}, "slack_action")

	httpClient := &http.Client{
		Timeout: time.Duration(c.cfg.HTTPTimeoutSeconds) * time.Second,
	}

	options := []slack.Option{
		slack.OptionDebug(c.cfg.DebugLogging),
		slack.OptionAppLevelToken(c.cfg.AppToken),
		slack.OptionLog(&slackApilogger{logger: c.logger}),
		slack.OptionHTTPClient(httpClient),
	}

	if c.apiURL != "" {
		options = append(options, slack.OptionAPIURL(c.apiURL))
	}

	c.api = slack.New(c.cfg.BotToken, options...)

	response, err := c.api.AuthTestContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed Slack authorization test: %w", err)
	}

	if response.UserID == "" {
		return nil, errors.New("missing bot user ID in auth test response")
	}

	c.botUserID = response.UserID
	c.botID = response.BotID

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

	ts, _, err := callAPIWithPolicy(ctx, c, c.logger.WithField("channel_id", channelID), action, callPolicy{outcomeUnknown: isPostOutcomeUnknown}, f)

	return ts, err
}

// ListBotMessages returns the top-level messages in a channel that were posted by this bot after oldest, and have
// message metadata with the given event type. Metadata is included in the returned messages.
// Messages are matched on the bot ID, since messages posted with as_user=false (e.g. with a custom username) are
// returned as bot_message without a user ID.
// An error wrapping [ErrTooManyMessages] is returned if the channel has too many messages since oldest to read within
// a bounded number of pages.
func (c *SlackAPIClient) ListBotMessages(ctx context.Context, channelID string, oldest time.Time, eventType string) ([]slack.Message, error) {
	if err := c.checkConnected(); err != nil {
		return nil, err
	}

	if channelID == "" {
		return nil, errors.New("channelID cannot be empty")
	}

	if c.botID == "" {
		return nil, errors.New("bot ID is unknown: the token is not a bot token")
	}

	action := "conversations.history"
	logger := c.logger.WithField("channel_id", channelID)

	params := &slack.GetConversationHistoryParameters{
		ChannelID:          channelID,
		Oldest:             FormatSlackTimestamp(oldest),
		Limit:              200,
		IncludeAllMetadata: true,
	}

	var result []slack.Message

	for range maxBotMessagesPages {
		f := func(ctx context.Context) (*slack.GetConversationHistoryResponse, any, error) {
			resp, err := c.api.GetConversationHistoryContext(ctx, params)
			return resp, nil, err
		}

		resp, _, err := callAPI(ctx, c, logger, action, f)
		if err != nil {
			return nil, err
		}

		for _, msg := range resp.Messages {
			if msg.BotID == c.botID && msg.Metadata.EventType == eventType {
				result = append(result, msg)
			}
		}

		if !resp.HasMore || resp.ResponseMetaData.NextCursor == "" {
			return result, nil
		}

		params.Cursor = resp.ResponseMetaData.NextCursor
	}

	return nil, fmt.Errorf("%w %s since %s to list bot messages", ErrTooManyMessages, channelID, oldest.Format(time.RFC3339))
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

	ts, _, err := callAPI(ctx, c, c.logger.WithField("channel_id", channelID), action, f)

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

	_, _, err := callAPI(ctx, c, c.logger.WithField("channel_id", channelID), action, f)

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

	_, _, err := callAPI(ctx, c, c.logger.WithField("channel_id", channelID), action, f)

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

	ts, _, err := callAPI(ctx, c, c.logger.WithField("channel_id", channelID), action, f)

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

// UpdateModal replaces the content of an open modal view, identified by its view ID.
// It fails (with a non-retryable error) if the user has closed the modal.
func (c *SlackAPIClient) UpdateModal(ctx context.Context, viewID string, request slack.ModalViewRequest) error {
	if err := c.checkConnected(); err != nil {
		return err
	}

	if viewID == "" {
		return errors.New("viewID cannot be empty")
	}

	action := "views.update"

	f := func(ctx context.Context) (any, any, error) {
		_, err := c.api.UpdateViewContext(ctx, request, "", "", viewID)
		return nil, nil, err
	}

	_, _, err := callAPI(ctx, c, c.logger.WithField("view_id", viewID), action, f)

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

	msgs, _, err := callAPI(ctx, c, c.logger.WithField("channel_id", channelID), action, f, SlackErrChannelNotFound, SlackErrThreadNotFound)
	if err != nil {
		if err.Error() == SlackErrChannelNotFound || err.Error() == SlackErrThreadNotFound {
			return false, nil
		}
		return false, err
	}

	return len(msgs) > 1, nil
}

// GetChannelInfo returns the Slack channel info (conversations.info) for a channel. Results are cached for
// [config.SlackClientConfig.ChannelInfoCacheTTL] (plus up to 20% jitter), and channel_not_found results for
// [config.SlackClientConfig.ChannelNotFoundCacheTTL]. A stale value is served for up to [channelInfoMaxStale] while
// conversations.info is rate limited. A missing channel is returned as an error with the text [SlackErrChannelNotFound].
func (c *SlackAPIClient) GetChannelInfo(ctx context.Context, channelID string) (*slack.Channel, error) {
	if err := c.checkConnected(); err != nil {
		return nil, err
	}

	if channelID == "" {
		return nil, errors.New("channelID cannot be empty")
	}

	action := "conversations.info"
	logger := c.logger.WithField("channel_id", channelID)
	ttl := c.cfg.ChannelInfoCacheTTL()

	policy := lookupPolicy{
		action:      action,
		key:         "GetChannelInfo:" + channelID,
		ttl:         ttl,
		jitter:      ttl / 5,
		notFoundTTL: c.cfg.ChannelNotFoundCacheTTL(),
		maxStale:    channelInfoMaxStale,
	}

	fetch := func(ctx context.Context, failFast bool) (*slack.Channel, bool, error) {
		f := func(ctx context.Context) (*slack.Channel, any, error) {
			input := slack.GetConversationInfoInput{
				ChannelID:     channelID,
				IncludeLocale: false,
			}
			val, err := c.api.GetConversationInfoContext(ctx, &input)
			return val, nil, err
		}

		policy := callPolicy{expectedErrors: []string{SlackErrChannelNotFound}, failFast: failFast}

		channel, _, err := callAPIWithPolicy(ctx, c, logger, action, policy, f)
		if err != nil {
			if err.Error() == SlackErrChannelNotFound {
				return nil, true, nil
			}
			return nil, false, err
		}

		return channel, false, nil
	}

	channel, notFound, err := cachedLookup(ctx, c, logger, policy, fetch)
	if err != nil {
		return nil, err
	}

	if notFound || channel == nil {
		return nil, errors.New(SlackErrChannelNotFound)
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
		chs, nextCursor, err := callAPI(ctx, c, c.logger, action, f)
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

// GetUserInfo returns the Slack user info (users.info) for a user. Results are cached for 1-2 hours, and a stale
// value is served for up to [userInfoMaxStale] while users.info is rate limited.
func (c *SlackAPIClient) GetUserInfo(ctx context.Context, userID string) (*slack.User, error) {
	if err := c.checkConnected(); err != nil {
		return nil, err
	}

	if userID == "" {
		return nil, errors.New("userID cannot be empty")
	}

	action := "users.info"

	policy := lookupPolicy{
		action:   action,
		key:      "GetUserInfo:" + userID,
		ttl:      time.Hour,
		jitter:   time.Hour,
		maxStale: userInfoMaxStale,
	}

	fetch := func(ctx context.Context, failFast bool) (*slack.User, bool, error) {
		f := func(ctx context.Context) (*slack.User, any, error) {
			val, err := c.api.GetUserInfoContext(ctx, userID)
			return val, nil, err
		}

		user, _, err := callAPIWithPolicy(ctx, c, c.logger, action, callPolicy{failFast: failFast}, f)

		return user, false, err
	}

	user, _, err := cachedLookup(ctx, c, c.logger, policy, fetch)
	if err != nil {
		return nil, err
	}

	return user, nil
}

// ListUserGroupMembers returns the user IDs in a Slack user group (usergroups.users.list). Results are cached for
// 1-1.5 minutes, and a stale value is served for up to [membershipMaxStale] while the method is rate limited.
// An unknown group (no_such_subteam) has no members.
func (c *SlackAPIClient) ListUserGroupMembers(ctx context.Context, groupID string) (map[string]struct{}, error) {
	if err := c.checkConnected(); err != nil {
		return nil, err
	}

	if groupID == "" {
		return nil, errors.New("groupID cannot be empty")
	}

	action := "usergroups.users.list"

	policy := lookupPolicy{
		action:      action,
		key:         "ListUserGroupMembers:" + groupID,
		ttl:         time.Minute,
		jitter:      30 * time.Second,
		notFoundTTL: time.Minute,
		maxStale:    membershipMaxStale,
	}

	fetch := func(ctx context.Context, failFast bool) (map[string]struct{}, bool, error) {
		f := func(ctx context.Context) ([]string, any, error) {
			val, err := c.api.GetUserGroupMembersContext(ctx, groupID)
			return val, nil, err
		}

		policy := callPolicy{expectedErrors: []string{SlackErrNoSuchSubTeam}, failFast: failFast}

		userIDs, _, err := callAPIWithPolicy(ctx, c, c.logger, action, policy, f)
		if err != nil {
			if err.Error() == SlackErrNoSuchSubTeam {
				return nil, true, nil
			}
			return nil, false, err
		}

		result := make(map[string]struct{}, len(userIDs))

		for _, userID := range userIDs {
			result[userID] = struct{}{}
		}

		return result, false, nil
	}

	result, _, err := cachedLookup(ctx, c, c.logger, policy, fetch)
	if err != nil {
		return nil, err
	}

	if result == nil {
		result = make(map[string]struct{})
	}

	return result, nil
}

// GetUserIDsInChannel returns the user IDs of the members of a Slack channel (conversations.members). Results are
// cached for 1-1.5 minutes, and a stale value is served for up to [membershipMaxStale] while the method is rate
// limited. An unknown channel (channel_not_found) has no members.
func (c *SlackAPIClient) GetUserIDsInChannel(ctx context.Context, channelID string) (map[string]struct{}, error) {
	if err := c.checkConnected(); err != nil {
		return nil, err
	}

	if channelID == "" {
		return nil, errors.New("channelID cannot be empty")
	}

	action := "conversations.members"
	logger := c.logger.WithField("channel_id", channelID)

	policy := lookupPolicy{
		action:      action,
		key:         "GetUserIDsInChannel:" + channelID,
		ttl:         time.Minute,
		jitter:      30 * time.Second,
		notFoundTTL: c.cfg.ChannelNotFoundCacheTTL(),
		maxStale:    membershipMaxStale,
	}

	fetch := func(ctx context.Context, failFast bool) (map[string]struct{}, bool, error) {
		params := &slack.GetUsersInConversationParameters{
			ChannelID: channelID,
		}

		f := func(ctx context.Context) ([]string, string, error) {
			return c.api.GetUsersInConversationContext(ctx, params)
		}

		policy := callPolicy{expectedErrors: []string{SlackErrChannelNotFound}, failFast: failFast}
		result := make(map[string]struct{})

		for {
			users, nextCursor, err := callAPIWithPolicy(ctx, c, logger, action, policy, f)
			if err != nil {
				if err.Error() == SlackErrChannelNotFound {
					return nil, true, nil
				}
				return nil, false, err
			}

			for _, userID := range users {
				result[userID] = struct{}{}
			}

			if nextCursor == "" {
				return result, false, nil
			}

			params.Cursor = nextCursor
		}
	}

	result, _, err := cachedLookup(ctx, c, logger, policy, fetch)
	if err != nil {
		return nil, err
	}

	if result == nil {
		result = make(map[string]struct{})
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

// callPolicy configures how [callAPIWithPolicy] handles errors.
type callPolicy struct {
	// expectedErrors are Slack error codes that are returned without counting them as errors or retrying.
	expectedErrors []string

	// outcomeUnknown, if set, reports errors where the call may still have succeeded. They are not retried,
	// and are returned wrapped in [ErrPostOutcomeUnknown].
	outcomeUnknown func(error) bool

	// failFast makes a lookup method fail immediately (instead of waiting) if it is backing off after a 429,
	// and return a 429 without retrying. Used when a stale cached value can be served instead.
	failFast bool
}

func callAPI[V any, W any](ctx context.Context, c *SlackAPIClient, logger types.Logger,
	action string, f func(ctx context.Context) (V, W, error), expectedErrors ...string,
) (V, W, error) {
	return callAPIWithPolicy(ctx, c, logger, action, callPolicy{expectedErrors: expectedErrors}, f)
}

// MaxCallDuration returns an upper bound for how long a Slack API call made by [SlackAPIClient] can take, including
// retries: the larger of the rate limit and transient error retry budgets, plus the HTTP timeout of the last attempt.
// Time spent waiting for a free concurrency slot (see [config.SlackClientConfig.Concurrency]) is not included.
func MaxCallDuration(cfg *config.SlackClientConfig) time.Duration {
	budget := max(cfg.MaxRateLimitErrorWaitTimeSeconds, cfg.MaxTransientErrorWaitTimeSeconds)
	return time.Duration(budget+cfg.HTTPTimeoutSeconds) * time.Second
}

// callAPIWithPolicy calls f, retrying rate limit and transient errors within the configured budgets.
// Lookup methods first wait for (or fail fast on) an active per-method rate limit backoff. A 429 records the
// per-method backoff, and signals the global rate limit gate for write methods only (see [RateLimitGroup]).
func callAPIWithPolicy[V any, W any](ctx context.Context, c *SlackAPIClient, logger types.Logger,
	action string, policy callPolicy, f func(ctx context.Context) (V, W, error),
) (V, W, error) {
	attempt := 1
	started := time.Now()
	rateLimitBudgetEnd := started.Add(time.Duration(c.cfg.MaxRateLimitErrorWaitTimeSeconds) * time.Second)

	for {
		var result1 V
		var result2 W

		// Wait for (or fail fast on) the method's own rate limit backoff before the call, rather than making a
		// call that is likely to be rate limited again.
		if err := c.awaitMethodBackoff(ctx, logger, action, rateLimitBudgetEnd, policy.failFast); err != nil {
			return result1, result2, err
		}

		// Acquire a semaphore slot before the HTTP call to limit concurrency.
		if c.sem != nil {
			if err := c.sem.Acquire(ctx, 1); err != nil {
				return result1, result2, err
			}
		}

		callStart := time.Now()
		val1, val2, err := f(ctx)
		c.metrics.Observe(slackAPICallDurationMetric, time.Since(callStart).Seconds(), action)

		if c.sem != nil {
			c.sem.Release(1)
		}

		logger.
			WithField("action", action).
			WithField("response_val_1", val1).
			WithField("response_val_2", val2).
			WithField("error", err).
			Debug("Slack API call response")

		if err == nil {
			return val1, val2, nil
		}

		// Checked before the context, since a request cancelled in flight may still have been processed.
		if policy.outcomeUnknown != nil && policy.outcomeUnknown(err) {
			c.metrics.CounterInc(slackAPIErrorsTotalMetric, action, getSlackErrorMetricLabel(err))
			return result1, result2, fmt.Errorf("%w: %w", ErrPostOutcomeUnknown, err)
		}

		if ctx.Err() != nil {
			return result1, result2, err
		}

		if len(policy.expectedErrors) > 0 && slices.Contains(policy.expectedErrors, err.Error()) {
			return result1, result2, err
		}

		errTypeMetricLabel := getSlackErrorMetricLabel(err)

		c.metrics.CounterInc(slackAPIErrorsTotalMetric, action, errTypeMetricLabel)

		// waitForAPIError returns nil if we should try again, after waiting an appropriate amount of time for the error type.
		// A returned error indicates either that the error was not a known recoverable type, or that we've
		// exceeded retry limits or context deadline and should not retry.
		if waitErr := c.waitForAPIError(ctx, started, logger, attempt, action, policy.failFast, err); waitErr != nil {
			return result1, result2, waitErr
		}

		c.metrics.CounterInc(slackAPIRetriesTotalMetric, action, errTypeMetricLabel)

		attempt++
	}
}

// waitForAPIError determines whether the given error is a rate limit error or a transient error,
// and if so waits an appropriate amount of time before the next retry attempt.
// A rate limit error is always signalled (see [SlackAPIClient.signalRateLimit]), also when it is not retried.
// An error is returned if the retry should not be attempted (e.g. if we've exceeded max attempts or context deadline).
func (c *SlackAPIClient) waitForAPIError(ctx context.Context, started time.Time, logger types.Logger, attempt int, action string, failFast bool, err error) error {
	if rateLimitError, ok := errors.AsType[*slack.RateLimitedError](err); ok {
		c.signalRateLimit(ctx, logger, action, rateLimitError.RetryAfter)

		remainingWaitTime := time.Until(started.Add(time.Duration(c.cfg.MaxRateLimitErrorWaitTimeSeconds) * time.Second))

		if failFast || attempt >= c.cfg.MaxAttemptsForRateLimitError || remainingWaitTime < time.Second {
			return fmt.Errorf("failed to call Slack API %s after %d attempts and %d seconds: rate limit error: %w", action, attempt, int(time.Since(started).Seconds()), err)
		}

		return waitForRateLimit(ctx, logger, rateLimitError, attempt, action, remainingWaitTime)
	}

	if IsTransientError(err) {
		remainingWaitTime := time.Until(started.Add(time.Duration(c.cfg.MaxTransientErrorWaitTimeSeconds) * time.Second))

		if attempt >= c.cfg.MaxAttemptsForTransientError || remainingWaitTime < time.Second {
			return fmt.Errorf("failed to call Slack API %s after %d attempts and %d seconds: transient error: %w", action, attempt, int(time.Since(started).Seconds()), err)
		}

		return waitForTransientError(ctx, logger, err, attempt, action, remainingWaitTime)
	}

	return err
}

func waitForRateLimit(ctx context.Context, logger types.Logger, err *slack.RateLimitedError, attempt int, action string, remainingWaitTime time.Duration) error {
	wait := min(err.RetryAfter+2*time.Second+jitter(500*time.Millisecond), remainingWaitTime)

	logger.WithField("action", action).WithField("wait", wait).WithField("attempt", attempt).Info("Slack API rate limit exceeded, waiting before retry")

	return sleep(ctx, wait)
}

func waitForTransientError(ctx context.Context, logger types.Logger, err error, attempt int, action string, remainingWaitTime time.Duration) error {
	wait := min(time.Duration(attempt)*time.Second+jitter(500*time.Millisecond), remainingWaitTime)

	logger.WithField("error", err).WithField("action", action).WithField("wait", wait).WithField("attempt", attempt).Info("Slack transient error, waiting before retry")

	return sleep(ctx, wait)
}

// jitter returns a random duration in [0, maxJitter) to spread retries across goroutines.
func jitter(maxJitter time.Duration) time.Duration {
	return time.Duration(rand.N(int64(maxJitter))) // #nosec G404 -- weak random is acceptable for retry jitter
}

func sleep(ctx context.Context, t time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(t):
		return nil
	}
}
