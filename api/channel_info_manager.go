package api

import (
	"context"
	"strings"
	"sync"
	"time"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/internal"
	"github.com/peteraglen/slack-manager/internal/slackapi"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

type channelInfoManager struct {
	slackClient           *slackapi.Client
	channelsLastSeen      map[string]time.Time
	detectedChannels      chan string
	channelInfoCache      map[string]*channelInfo
	allManagerChannels    []*internal.ChannelSummary
	allManagerChannelsMap map[string]*internal.ChannelSummary
	cacheLock             *sync.RWMutex
	logger                common.Logger
}

type channelInfo struct {
	ChannelExists      bool
	ChannelIsArchived  bool
	ManagerIsInChannel bool
	UserIDs            map[string]struct{}
	UserCount          int
}

func newChannelInfoManager(slackClient *slackapi.Client, logger common.Logger) *channelInfoManager {
	return &channelInfoManager{
		slackClient:      slackClient,
		channelsLastSeen: make(map[string]time.Time),
		detectedChannels: make(chan string, 10000),
		channelInfoCache: make(map[string]*channelInfo),
		cacheLock:        &sync.RWMutex{},
		logger:           logger,
	}
}

func (c *channelInfoManager) Init(ctx context.Context) error {
	if err := c.refreshAllManagerChannelsMap(ctx); err != nil {
		return err
	}

	c.logger.Infof("Found %d channels managed by Slack Manager", len(c.allManagerChannels))

	return nil
}

func (c *channelInfoManager) Run(ctx context.Context) {
	refreshChannelInfoInterval := 30 * time.Second
	refreshAllManagerChannelsInterval := 5 * time.Minute
	pruneInterval := 30 * time.Minute

	refreshChannelInfo := time.After(refreshChannelInfoInterval)
	refreshAllManagerChannels := time.After(refreshAllManagerChannelsInterval)
	prune := time.After(pruneInterval)

	for {
		select {
		case <-ctx.Done():
			return
		case channel := <-c.detectedChannels:
			c.channelsLastSeen[channel] = time.Now()
		case <-refreshChannelInfo:
			if err := c.refreshData(ctx); err != nil {
				c.logger.Errorf("Failed to refresh Slack channel info: %s", err)
			}
			refreshChannelInfo = time.After(refreshChannelInfoInterval)
		case <-refreshAllManagerChannels:
			if err := c.refreshAllManagerChannelsMap(ctx); err != nil {
				c.logger.Errorf("Failed to refresh Slack manager channel list: %s", err)
			}
			refreshAllManagerChannels = time.After(refreshAllManagerChannelsInterval)
		case <-prune:
			c.pruneInactiveChannels()
			prune = time.After(pruneInterval)
		}
	}
}

// MapChannelNameToIDIfNeeded maps a channel name to a channel ID, if needed. It the input value is a channel ID, it is returned unchanged.
// This ensures that alert clients may use both channel names and channel IDs interchangeably.
func (c *channelInfoManager) MapChannelNameToIDIfNeeded(channelName string) string {
	if channelName == "" {
		return ""
	}

	if channel, found := c.allManagerChannelsMap[channelName]; found {
		return channel.ID
	}

	return channelName
}

func (c *channelInfoManager) GetChannelInfo(ctx context.Context, channel string) (*channelInfo, error) {
	// Refresh the last seen timestamp for the channel, to ensure continued caching
	if err := internal.TrySend(ctx, channel, c.detectedChannels); err != nil {
		return nil, err
	}

	// First try to find cached data
	if info, found := c.getCachedInfo(channel); found {
		return info, nil
	}

	// No cached data found - fetch data directly from Slack
	info, err := c.refreshChannelInfo(ctx, channel)

	return info, err
}

func (c *channelInfoManager) ManagedChannels() []*internal.ChannelSummary {
	return c.allManagerChannels
}

func (c *channelInfoManager) getCachedInfo(channel string) (*channelInfo, bool) {
	c.cacheLock.RLock()
	defer c.cacheLock.RUnlock()

	if info, found := c.channelInfoCache[channel]; found {
		return info, true
	}

	return nil, false
}

func (c *channelInfoManager) refreshData(ctx context.Context) error {
	sem := semaphore.NewWeighted(3)
	errg, ctx := errgroup.WithContext(ctx)

	for _channel := range c.channelsLastSeen {
		channel := _channel

		errg.Go(func() error {
			if err := sem.Acquire(ctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)

			if _, err := c.refreshChannelInfo(ctx, channel); err != nil {
				return err
			}

			return nil
		})
	}

	return errg.Wait()
}

func (c *channelInfoManager) refreshChannelInfo(ctx context.Context, channel string) (*channelInfo, error) {
	channelFound := true

	slackChannel, err := c.slackClient.GetChannelInfo(ctx, channel)
	if err != nil {
		if strings.Contains(err.Error(), "channel_not_found") {
			channelFound = false
		} else {
			return nil, err
		}
	}

	var info *channelInfo

	if channelFound {
		users, err := c.slackClient.GetUserIDsInChannel(ctx, channel)
		if err != nil {
			return nil, err
		}

		managerIsInChannel, err := c.slackClient.BotIsInChannel(ctx, channel)
		if err != nil {
			return nil, err
		}

		info = &channelInfo{
			ChannelExists:      true,
			ChannelIsArchived:  slackChannel.IsArchived,
			ManagerIsInChannel: managerIsInChannel,
			UserIDs:            users,
			UserCount:          len(users),
		}
	} else {
		info = &channelInfo{ChannelExists: false}
	}

	c.cacheLock.Lock()
	defer c.cacheLock.Unlock()

	c.channelInfoCache[channel] = info

	return info, nil
}

func (c *channelInfoManager) pruneInactiveChannels() {
	for channel, lastSeen := range c.channelsLastSeen {
		if time.Since(lastSeen) > 12*time.Hour {
			delete(c.channelsLastSeen, channel)
		}
	}
}

func (c *channelInfoManager) refreshAllManagerChannelsMap(ctx context.Context) error {
	allManagerChannels, err := c.slackClient.ListBotChannels(ctx)
	if err != nil {
		return err
	}

	allManagerChannelsMap := make(map[string]*internal.ChannelSummary)

	for _, channel := range allManagerChannels {
		allManagerChannelsMap[channel.ID] = channel
		allManagerChannelsMap[channel.Name] = channel
	}

	c.allManagerChannels = allManagerChannels
	c.allManagerChannelsMap = allManagerChannelsMap

	return nil
}
