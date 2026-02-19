package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/eko/gocache/lib/v4/store"
	"github.com/slackmgr/core/config"
	"github.com/slackmgr/core/internal"
	"github.com/slackmgr/core/manager/internal/models"
	"github.com/slackmgr/types"
)

type dbCacheMiddleware struct {
	db                          types.DB
	issueHashCache              *internal.Cache
	channelProcessingStateCache *internal.Cache
	moveMappingCache            *internal.Cache
}

func newDBCacheMiddleware(db types.DB, cacheStore store.StoreInterface, logger types.Logger, cfg config.ManagerConfig) *dbCacheMiddleware {
	cacheKeyPrefix := cfg.CacheKeyPrefix + "db-cache:"

	issueHashCache := internal.NewCache(cacheStore, cacheKeyPrefix+"issueHash:", logger)
	channelProcessinStateCache := internal.NewCache(cacheStore, cacheKeyPrefix+"channelProcessingState:", logger)
	moveMappingCache := internal.NewCache(cacheStore, cacheKeyPrefix+"moveMapping:", logger)

	return &dbCacheMiddleware{
		db:                          db,
		issueHashCache:              issueHashCache,
		channelProcessingStateCache: channelProcessinStateCache,
		moveMappingCache:            moveMappingCache,
	}
}

func (d *dbCacheMiddleware) Init(_ context.Context, _ bool) error {
	return errors.New("dbCacheMiddleware does not support the Init method")
}

func (d *dbCacheMiddleware) SaveAlert(ctx context.Context, alert *types.Alert) error {
	return d.db.SaveAlert(ctx, alert)
}

func (d *dbCacheMiddleware) SaveIssue(ctx context.Context, issue types.Issue) error {
	return d.SaveIssues(ctx, issue)
}

func (d *dbCacheMiddleware) SaveIssues(ctx context.Context, issues ...types.Issue) error {
	if len(issues) == 0 {
		return nil
	}

	issuesToUpdate := []types.Issue{}
	issueHashes := make(map[string]string)
	issueModels := []*models.Issue{}

	// Loop through all issues and check if they have changed.
	for _, issue := range issues {
		issueModel, ok := issue.(*models.Issue)
		if !ok {
			return fmt.Errorf("expected issue to be of type *internal.Issue, got %T", issue)
		}

		issueModels = append(issueModels, issueModel)

		// Marshal the issue body to JSON and cache it internally.
		body, err := issueModel.MarshalJSONAndCache()
		if err != nil {
			return fmt.Errorf("failed to marshal issue body: %w", err)
		}

		cacheKey := issue.UniqueID()
		issueHash := string(internal.HashBytes(body))
		issueHashes[cacheKey] = issueHash

		// No point in updating db if the issue has not changed.
		if existingHash, ok := d.issueHashCache.Get(ctx, cacheKey); ok && existingHash == issueHash {
			continue
		}

		// The issue has changed, so we need to update it in the database.
		issuesToUpdate = append(issuesToUpdate, issue)

		// We must explicitly remove the old issue hash from the cache, since the upcoming cache Set may fail
		// after we save the issue to the database, thus leaving the cache in an inconsistent state.
		if err := d.issueHashCache.Delete(ctx, cacheKey); err != nil {
			return fmt.Errorf("failed to delete issue hash from cache: %w", err)
		}
	}

	// Ensure cached JSON body is reset after we are done.
	defer func() {
		for _, issueModel := range issueModels {
			issueModel.ResetCachedJSONBody()
		}
	}()

	// No changed issues found.
	if len(issuesToUpdate) == 0 {
		return nil
	}

	if len(issuesToUpdate) == 1 {
		if err := d.db.SaveIssue(ctx, issuesToUpdate[0]); err != nil {
			return fmt.Errorf("failed to save issues in database: %w", err)
		}
	} else {
		if err := d.db.SaveIssues(ctx, issuesToUpdate...); err != nil {
			return fmt.Errorf("failed to update issues in database: %w", err)
		}
	}

	// Update the cache with all issue hashes (both unchanged and changed).
	for cacheKey, issueHash := range issueHashes {
		d.issueHashCache.Set(ctx, cacheKey, issueHash, 30*24*time.Hour)
	}

	return nil
}

func (d *dbCacheMiddleware) MoveIssue(ctx context.Context, issue types.Issue, sourceChannelID, targetChannelID string) error {
	issueModel, ok := issue.(*models.Issue)
	if !ok {
		return fmt.Errorf("expected issue to be of type *internal.Issue, got %T", issue)
	}

	// Marshal the issue body to JSON and cache it internally.
	body, err := issueModel.MarshalJSONAndCache()
	if err != nil {
		return fmt.Errorf("failed to marshal issue body: %w", err)
	}

	cacheKey := issue.UniqueID()
	issueHash := string(internal.HashBytes(body))

	// We must explicitly remove the old issue hash from the cache, since the upcoming cache Set may fail
	// after we save the issue to the database, thus leaving the cache in an inconsistent state.
	if err := d.issueHashCache.Delete(ctx, cacheKey); err != nil {
		return fmt.Errorf("failed to delete issue hash from cache: %w", err)
	}

	defer issueModel.ResetCachedJSONBody()

	if err := d.db.MoveIssue(ctx, issue, sourceChannelID, targetChannelID); err != nil {
		return fmt.Errorf("failed to move issue from channel %s to %s: %w", sourceChannelID, targetChannelID, err)
	}

	// After moving the issue, we need to update the cache with the new issue hash.
	d.issueHashCache.Set(ctx, cacheKey, issueHash, 30*24*time.Hour)

	return nil
}

func (d *dbCacheMiddleware) FindOpenIssueByCorrelationID(ctx context.Context, channelID, correlationID string) (string, json.RawMessage, error) {
	return d.db.FindOpenIssueByCorrelationID(ctx, channelID, correlationID)
}

func (d *dbCacheMiddleware) FindIssueBySlackPostID(ctx context.Context, channelID, postID string) (string, json.RawMessage, error) {
	return d.db.FindIssueBySlackPostID(ctx, channelID, postID)
}

func (d *dbCacheMiddleware) FindActiveChannels(ctx context.Context) ([]string, error) {
	return d.db.FindActiveChannels(ctx)
}

func (d *dbCacheMiddleware) LoadOpenIssuesInChannel(ctx context.Context, channelID string) (map[string]json.RawMessage, error) {
	issueBodies, err := d.db.LoadOpenIssuesInChannel(ctx, channelID)
	if err != nil {
		return nil, err
	}

	// Cache the issue body hashes to avoid unnecessary write operations.
	for id, body := range issueBodies {
		d.issueHashCache.Set(ctx, id, string(internal.HashBytes(body)), 30*24*time.Hour)
	}

	return issueBodies, nil
}

func (d *dbCacheMiddleware) SaveMoveMapping(ctx context.Context, moveMapping types.MoveMapping) error {
	body, err := moveMapping.MarshalJSON()
	if err != nil {
		return fmt.Errorf("failed to marshal move mapping: %w", err)
	}

	if err := d.db.SaveMoveMapping(ctx, moveMapping); err != nil {
		return err
	}

	cacheKey := moveMappingCacheKey(moveMapping.ChannelID(), moveMapping.GetCorrelationID())

	d.moveMappingCache.Set(ctx, cacheKey, string(body), 24*time.Hour)

	return nil
}

func (d *dbCacheMiddleware) FindMoveMapping(ctx context.Context, channelID, correlationID string) (json.RawMessage, error) {
	cacheKey := moveMappingCacheKey(channelID, correlationID)

	if mapping, ok := d.moveMappingCache.Get(ctx, cacheKey); ok {
		if mapping != "" {
			return json.RawMessage(mapping), nil
		}
		return nil, nil
	}

	mapping, err := d.db.FindMoveMapping(ctx, channelID, correlationID)
	if err != nil {
		return nil, err
	}

	// If no mapping is found, we still cache an empty string to avoid unnecessary database lookups.
	if mapping != nil {
		d.moveMappingCache.Set(ctx, cacheKey, string(mapping), 24*time.Hour)
	} else {
		d.moveMappingCache.Set(ctx, cacheKey, "", 24*time.Hour)
	}

	return mapping, nil
}

func (d *dbCacheMiddleware) DeleteMoveMapping(ctx context.Context, channelID, correlationID string) error {
	cacheKey := moveMappingCacheKey(channelID, correlationID)

	// Remove the mapping from the cache first.
	if err := d.moveMappingCache.Delete(ctx, cacheKey); err != nil {
		return fmt.Errorf("failed to delete move mapping from cache: %w", err)
	}

	// Then remove it from the database.
	if err := d.db.DeleteMoveMapping(ctx, channelID, correlationID); err != nil {
		return fmt.Errorf("failed to delete move mapping from database: %w", err)
	}

	return nil
}

func (d *dbCacheMiddleware) SaveChannelProcessingState(ctx context.Context, state *types.ChannelProcessingState) error {
	if state == nil {
		return errors.New("channel processing state cannot be nil")
	}

	if err := d.db.SaveChannelProcessingState(ctx, state); err != nil {
		return err
	}

	jsonBody, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal channel processing state: %w", err)
	}

	d.channelProcessingStateCache.Set(ctx, state.ChannelID, string(jsonBody), 24*time.Hour)

	return nil
}

func (d *dbCacheMiddleware) FindChannelProcessingState(ctx context.Context, channelID string) (*types.ChannelProcessingState, error) {
	if cachedJSONBody, ok := d.channelProcessingStateCache.Get(ctx, channelID); ok {
		var state types.ChannelProcessingState

		if err := json.Unmarshal([]byte(cachedJSONBody), &state); err != nil {
			return nil, fmt.Errorf("failed to unmarshal channel processing state from cache: %w", err)
		}

		return &state, nil
	}

	state, err := d.db.FindChannelProcessingState(ctx, channelID)
	if err != nil {
		return nil, err
	}

	if state != nil {
		jsonBody, err := json.Marshal(state)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal channel processing state: %w", err)
		}

		d.channelProcessingStateCache.Set(ctx, channelID, string(jsonBody), 24*time.Hour)
	}

	return state, nil
}

func (d *dbCacheMiddleware) DropAllData(_ context.Context) error {
	return errors.New("dbCacheMiddleware does not support the DropAllData method")
}

// moveMappingCacheKey generates a cache key for the move mapping based on channel ID and correlation ID.
// The correlation key is user-defined, so we hash the key to ensure that it doesn't violate any key requirements.
func moveMappingCacheKey(channelID, correlationID string) string {
	return internal.Hash(channelID + ":" + correlationID)
}
