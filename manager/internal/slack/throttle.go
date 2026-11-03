package slack

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/slack-go/slack"
	"github.com/slackmgr/core/internal/models"
	"github.com/slackmgr/types"
)

// postBlockKind classifies a block of an issue post by how important a change to it is to the reader.
type postBlockKind int

const (
	// postBlockSignificant is a block where a change must be shown immediately (status, header, buttons, user actions).
	postBlockSignificant postBlockKind = iota

	// postBlockContent is a block with alert content (text, fields, author, host, link, footer).
	postBlockContent

	// postBlockStats is the footer block with alert statistics (first and last alert time, alert count). It changes
	// with every alert, and is not part of the post fingerprint.
	postBlockStats
)

// updateTier is the throttle tier of a post update, decided by what changed since the post was last updated.
type updateTier string

const (
	// updateTierSignificant is an update with a significant change, or a new post. It is never throttled.
	updateTierSignificant updateTier = "significant"

	// updateTierContent is an update where the alert content changed. It is throttled for issues where the content
	// changes frequently (see [contentThrottleWindow]).
	updateTierContent updateTier = "content"

	// updateTierFooter is an update where only the footer statistics changed. It is throttled for the max throttle duration.
	updateTierFooter updateTier = "footer"
)

// postBlock is a block of an issue post, with its kind.
type postBlock struct {
	kind  postBlockKind
	block slack.Block
}

// postBlocks is the ordered list of blocks of an issue post.
type postBlocks []postBlock

// add appends the blocks with the given kind.
func (b *postBlocks) add(kind postBlockKind, blocks ...slack.Block) {
	for _, block := range blocks {
		*b = append(*b, postBlock{kind: kind, block: block})
	}
}

// blocks returns the Slack blocks, in order.
func (b *postBlocks) blocks() []slack.Block {
	blocks := make([]slack.Block, 0, len(*b))

	for _, pb := range *b {
		blocks = append(blocks, pb.block)
	}

	return blocks
}

// ofKind returns the Slack blocks of the given kind, in order.
func (b *postBlocks) ofKind(kind postBlockKind) []slack.Block {
	blocks := []slack.Block{}

	for _, pb := range *b {
		if pb.kind == kind {
			blocks = append(blocks, pb.block)
		}
	}

	return blocks
}

// significantPostParts is the input to the significant hash of a post fingerprint.
type significantPostParts struct {
	Action    models.SlackAction `json:"action"`
	Severity  string             `json:"severity"`
	Username  string             `json:"username"`
	IconEmoji string             `json:"iconEmoji"`
	Blocks    []slack.Block      `json:"blocks"`
}

// contentPostParts is the input to the content hash of a post fingerprint.
type contentPostParts struct {
	FallbackText string        `json:"fallbackText"`
	Blocks       []slack.Block `json:"blocks"`
}

// postFingerprint returns the fingerprint of the issue post with the given blocks (see [models.SlackPostFingerprint]).
// A zero fingerprint is returned if the post cannot be hashed, which makes the next update significant.
func postFingerprint(issue *models.Issue, action models.SlackAction, blocks *postBlocks) models.SlackPostFingerprint {
	significant, err := hashJSON(significantPostParts{
		Action:    action,
		Severity:  string(issue.LastAlert.Severity),
		Username:  issue.LastAlert.Username,
		IconEmoji: issue.LastAlert.IconEmoji,
		Blocks:    blocks.ofKind(postBlockSignificant),
	})
	if err != nil {
		return models.SlackPostFingerprint{}
	}

	content, err := hashJSON(contentPostParts{
		FallbackText: issue.LastAlert.FallbackText,
		Blocks:       blocks.ofKind(postBlockContent),
	})
	if err != nil {
		return models.SlackPostFingerprint{}
	}

	return models.SlackPostFingerprint{Significant: significant, Content: content}
}

// hashJSON returns a hex encoded SHA-256 hash of the JSON encoding of v.
func hashJSON(v any) (string, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(body)

	return hex.EncodeToString(sum[:16]), nil
}

// throttleUpdate decides whether the update of an existing issue post is throttled, i.e. postponed to a later update
// attempt, and returns the update tier. A throttled update is not lost: the issue keeps its update flag, and the post
// is updated by a later attempt (typically the next channel processing run) once the throttle window has passed.
//
// Throttling limits the Slack post update rate for issues that receive alerts frequently, since Slack allows about
// 50 post updates per minute per workspace. Updates are classified by what changed since the post was last updated:
//   - Significant changes are never throttled: a new post, a changed action (alert or resolve), active mentions, or a
//     change in a significant part of the post (see [models.SlackPostFingerprint.Significant]).
//   - Content changes are throttled by [contentThrottleWindow], measured from the last content change.
//   - Footer-only changes are throttled by the max throttle duration, measured from the last post update.
func (c *Client) throttleUpdate(issue *models.Issue, action models.SlackAction) (updateTier, bool) {
	if !issue.HasSlackPost() ||
		issue.SlackPostID == PostIDInvalidChannel ||
		action != issue.SlackPostLastAction ||
		issue.SlackPostFingerprint.IsZero() ||
		issue.LastAlertHasActiveMentions() {
		return updateTierSignificant, false
	}

	fingerprint := postFingerprint(issue, action, c.getPostBlocks(issue, action, postUpdateMethodUpdate))

	if fingerprint.IsZero() || fingerprint.Significant != issue.SlackPostFingerprint.Significant {
		return updateTierSignificant, false
	}

	settings := c.managerSettings.GetSettings()
	maxWindow := time.Duration(settings.MaxThrottleDurationSeconds) * time.Second

	if fingerprint.Content == issue.SlackPostFingerprint.Content {
		return updateTierFooter, time.Since(issue.SlackPostUpdated) < maxWindow
	}

	minWindow := time.Duration(settings.MinThrottleDurationSeconds) * time.Second
	window := contentThrottleWindow(issue, minWindow, maxWindow)

	return updateTierContent, time.Since(issue.SlackPostContentUpdated) < window
}

// contentThrottleWindow returns the minimum time between content changes of the issue post. The window starts at
// minWindow (capped to maxWindow), and is twice the gap between the last two content changes while the content keeps
// changing, between minWindow and maxWindow. A stream of content changes is thus shown after 0, ~10, ~30, ~70 seconds
// and then about once per maxWindow (with the default settings). The window is reset to minWindow once the gap between
// the last two content changes is at least twice maxWindow, i.e. when the content has been stable for a while.
func contentThrottleWindow(issue *models.Issue, minWindow, maxWindow time.Duration) time.Duration {
	minWindow = min(minWindow, maxWindow)

	if issue.SlackPostPrevContentUpdated.IsZero() {
		return minWindow
	}

	gap := issue.SlackPostContentUpdated.Sub(issue.SlackPostPrevContentUpdated)

	if gap >= 2*maxWindow {
		return minWindow
	}

	return min(max(2*gap, minWindow), maxWindow)
}

// throttled reports whether the post update is throttled (see [Client.throttleUpdate]), and records it if so.
func (c *Client) throttled(issue *models.Issue, action models.SlackAction) bool {
	tier, throttled := c.throttleUpdate(issue, action)
	if !throttled {
		return false
	}

	c.logger.WithFields(issue.LogFields()).WithField("throttle_tier", string(tier)).Debug("Throttle Slack post")
	c.recordPost(slackPostResultThrottled, types.InsightsEventSlackPostThrottled, string(action), issue)

	return true
}
