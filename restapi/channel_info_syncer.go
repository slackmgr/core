package restapi

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/slackmgr/core/internal"
	"github.com/slackmgr/types"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

// ChannelInfoProvider defines the interface for channel information operations.
type ChannelInfoProvider interface {
	GetChannelInfo(ctx context.Context, channel string) (*ChannelInfo, error)
	MapChannelNameToIDIfNeeded(channelName string) string
	ManagedChannels() []*internal.ChannelSummary
}

// channelInfoSyncer periodically syncs Slack channel information and caches it.
type channelInfoSyncer struct {
	slackClient      SlackClient
	channelsLastSeen map[string]time.Time
	detectedChannels chan string
	channelInfoCache map[string]*ChannelInfo
	managedChannels  atomic.Pointer[managedChannelsData]
	cacheLock        *sync.RWMutex
	logger           types.Logger
}

// managedChannelsData holds the managed channels data for atomic swapping.
type managedChannelsData struct {
	list   []*internal.ChannelSummary
	byName map[string]*internal.ChannelSummary
}

// ChannelInfo contains information about a Slack channel.
type ChannelInfo struct {
	ChannelExists      bool
	ChannelIsArchived  bool
	ManagerIsInChannel bool
	UserCount          int
}

func newChannelInfoSyncer(slackClient SlackClient, logger types.Logger) *channelInfoSyncer {
	return &channelInfoSyncer{
		slackClient:      slackClient,
		channelsLastSeen: make(map[string]time.Time),
		detectedChannels: make(chan string, 10000),
		channelInfoCache: make(map[string]*ChannelInfo),
		cacheLock:        &sync.RWMutex{},
		logger:           logger,
	}
}

func (c *channelInfoSyncer) Init(ctx context.Context) error {
	if err := c.refreshAllManagedChannelsMap(ctx); err != nil {
		return err
	}

	data := c.managedChannels.Load()
	if data != nil {
		c.logger.Infof("Found %d channels managed by Slack Manager", len(data.list))
	}

	return nil
}

func (c *channelInfoSyncer) Run(ctx context.Context) error {
	c.logger.Info("Slack channel info syncer started")
	defer c.logger.Info("Slack channel info syncer exited")

	refreshChannelInfoInterval := 30 * time.Second
	refreshAllManagedChannelsInterval := 5 * time.Minute
	pruneInterval := 30 * time.Minute

	refreshChannelInfo := time.After(refreshChannelInfoInterval)
	refreshAllManagedChannels := time.After(refreshAllManagedChannelsInterval)
	prune := time.After(pruneInterval)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case channel := <-c.detectedChannels:
			c.channelsLastSeen[channel] = time.Now()
		case <-refreshChannelInfo:
			if err := c.refreshData(ctx); err != nil {
				c.logger.Errorf("Failed to refresh Slack channel info: %s", err)
			}

			refreshChannelInfo = time.After(refreshChannelInfoInterval)
		case <-refreshAllManagedChannels:
			if err := c.refreshAllManagedChannelsMap(ctx); err != nil {
				c.logger.Errorf("Failed to refresh Slack manager channel list: %s", err)
			}

			refreshAllManagedChannels = time.After(refreshAllManagedChannelsInterval)
		case <-prune:
			c.pruneInactiveChannels()
			prune = time.After(pruneInterval)
		}
	}
}

// MapChannelNameToIDIfNeeded maps a channel name to a channel ID, if needed. It the input value is a channel ID, it is returned unchanged.
// This ensures that alert clients may use both channel names and channel IDs interchangeably.
func (c *channelInfoSyncer) MapChannelNameToIDIfNeeded(channelName string) string {
	if channelName == "" {
		return ""
	}

	// Try to find a mapping from name to ID
	data := c.managedChannels.Load()

	if data != nil {
		if channel, found := data.byName[strings.ToLower(channelName)]; found {
			return channel.ID
		}
	}

	// Default to returning the original value, which is probably a channel ID, if no mapping found
	return channelName
}

func (c *channelInfoSyncer) GetChannelInfo(ctx context.Context, channel string) (*ChannelInfo, error) {
	// Refresh the last seen timestamp for the channel, to ensure continued caching
	if err := internal.TrySend(ctx, channel, c.detectedChannels); err != nil {
		return nil, err
	}

	// First try to find cached data
	if info, found := c.getCachedInfo(channel); found {
		return info, nil
	}

	// No cached data found - fetch data directly from Slack
	return c.refreshChannelInfo(ctx, channel)
}

func (c *channelInfoSyncer) ManagedChannels() []*internal.ChannelSummary {
	data := c.managedChannels.Load()
	if data == nil {
		return nil
	}
	return data.list
}

func (c *channelInfoSyncer) getCachedInfo(channel string) (*ChannelInfo, bool) {
	c.cacheLock.RLock()
	defer c.cacheLock.RUnlock()

	if info, found := c.channelInfoCache[channel]; found {
		return info, true
	}

	return nil, false
}

func (c *channelInfoSyncer) refreshData(ctx context.Context) error {
	sem := semaphore.NewWeighted(3)
	errg, ctx := errgroup.WithContext(ctx)

	for channel := range c.channelsLastSeen {
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

func (c *channelInfoSyncer) refreshChannelInfo(ctx context.Context, channel string) (*ChannelInfo, error) {
	channelFound := true

	slackChannel, err := c.slackClient.GetChannelInfo(ctx, channel)
	if err != nil {
		if err.Error() == internal.SlackChannelNotFoundError {
			channelFound = false
		} else {
			return nil, err
		}
	}

	var info *ChannelInfo

	if channelFound {
		users, err := c.slackClient.GetUserIDsInChannel(ctx, channel)
		if err != nil {
			return nil, err
		}

		managerIsInChannel, err := c.slackClient.BotIsInChannel(ctx, channel)
		if err != nil {
			return nil, err
		}

		info = &ChannelInfo{
			ChannelExists:      true,
			ChannelIsArchived:  slackChannel.IsArchived,
			ManagerIsInChannel: managerIsInChannel,
			UserCount:          len(users),
		}
	} else {
		info = &ChannelInfo{ChannelExists: false}
	}

	c.cacheLock.Lock()
	defer c.cacheLock.Unlock()

	c.channelInfoCache[channel] = info

	return info, nil
}

func (c *channelInfoSyncer) pruneInactiveChannels() {
	for channel, lastSeen := range c.channelsLastSeen {
		if time.Since(lastSeen) > 12*time.Hour {
			delete(c.channelsLastSeen, channel)
		}
	}
}

func (c *channelInfoSyncer) refreshAllManagedChannelsMap(ctx context.Context) error {
	list, err := c.slackClient.ListBotChannels(ctx)
	if err != nil {
		return err
	}

	byName := make(map[string]*internal.ChannelSummary)

	// Build lookup map by lower-cased channel name
	for _, channel := range list {
		byName[strings.ToLower(channel.Name)] = channel
	}

	c.managedChannels.Store(&managedChannelsData{
		list:   list,
		byName: byName,
	})

	return nil
}
