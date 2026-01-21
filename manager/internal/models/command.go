package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	commonlib "github.com/peteraglen/slack-manager-common"
)

// CommandAction represents the type of action a user has initiated within Slack.
type CommandAction string

const (
	CommandActionTerminateIssue         = CommandAction("terminate_issue")
	CommandActionResolveIssue           = CommandAction("resolve_issue")
	CommandActionUnresolveIssue         = CommandAction("unresolve_issue")
	CommandActionInvestigateIssue       = CommandAction("investigate_issue")
	CommandActionUninvestigateIssue     = CommandAction("uninvestigate_issue")
	CommandActionMuteIssue              = CommandAction("mute_issue")
	CommandActionUnmuteIssue            = CommandAction("unmute_issue")
	CommandActionMoveIssue              = CommandAction("move_issue")
	CommandActionCreateIssue            = CommandAction("create_issue")
	CommandActionShowIssueOptionButtons = CommandAction("show_issue_option_buttons")
	CommandActionHideIssueOptionButtons = CommandAction("hide_issue_option_buttons")
	CommandActionWebhook                = CommandAction("webhook")
)

// Command represents a user-initiated action within Slack that the system needs to process.
type Command struct {
	Timestamp               time.Time             `json:"timestamp"`
	SlackChannelID          string                `json:"slackChannelId,omitempty"`
	SlackPostID             string                `json:"ts,omitempty"`
	Reaction                string                `json:"reaction,omitempty"`
	UserID                  string                `json:"userId,omitempty"`
	UserRealName            string                `json:"userRealName,omitempty"`
	Action                  CommandAction         `json:"action,omitempty"`
	Parameters              map[string]any        `json:"parameters,omitempty"`
	WebhookParameters       *WebhookCommandParams `json:"webhookParameters,omitempty"`
	IncludeArchivedIssues   bool                  `json:"includeArchivedIssues"`
	ExecuteWhenNoIssueFound bool                  `json:"executeWhenNoIssueFound"`

	ack  func()
	nack func()
}

// WebhookCommandParams holds parameters specific to webhook commands.
type WebhookCommandParams struct {
	WebhookID     string              `json:"webhookId"`
	Input         map[string]string   `json:"input,omitempty"`
	CheckboxInput map[string][]string `json:"checkboxInput,omitempty"`
}

// NewCommandFromQueueItem creates a Command instance from a FIFO queue item.
// It unmarshals the JSON body of the queue item into a Command struct.
func NewCommandFromQueueItem(queueItem *commonlib.FifoQueueItem) (InFlightMessage, error) { //nolint:ireturn
	if len(queueItem.Body) == 0 {
		return nil, errors.New("command body is empty")
	}

	var cmd Command

	if err := json.Unmarshal([]byte(queueItem.Body), &cmd); err != nil {
		return nil, fmt.Errorf("failed to unmarshal message body: %w", err)
	}

	cmd.ack = queueItem.Ack
	cmd.nack = queueItem.Nack

	return &cmd, nil
}

// NewCommand creates a new Command instance with the provided parameters.
func NewCommand(slackChannelID, ts, reaction, userID, userRealName string, action CommandAction, parameters map[string]any) *Command {
	return &Command{
		Timestamp:      time.Now().UTC(),
		SlackChannelID: slackChannelID,
		SlackPostID:    ts,
		Reaction:       reaction,
		UserID:         userID,
		UserRealName:   userRealName,
		Action:         action,
		Parameters:     parameters,
	}
}

// Ack acknowledges the command message, indicating successful processing.
// Any subsequent calls to Ack or Nack will have no effect.
func (c *Command) Ack() {
	if c.ack != nil {
		c.ack()
	}

	c.ack = nil
	c.nack = nil
}

// Nack negatively acknowledges the command message, indicating processing failure and requesting re-delivery.
// Any subsequent calls to Ack or Nack will have no effect.
func (c *Command) Nack() {
	if c.nack != nil {
		c.nack()
	}

	c.ack = nil
	c.nack = nil
}

// DedupID generates a deduplication ID for the command based on its key attributes.
func (c *Command) DedupID() string {
	return fmt.Sprintf("command::%s::%s::%s", c.SlackChannelID, c.Action, c.Timestamp.Format(time.RFC3339Nano))
}

// LogFields returns a map of key-value pairs representing the Command's important fields for logging purposes.
func (c *Command) LogFields() map[string]any {
	if c == nil {
		return nil
	}

	fields := map[string]any{
		"channel_id":    c.SlackChannelID,
		"slack_post_id": c.SlackPostID,
		"reaction":      c.Reaction,
		"action":        c.Action,
		"user_id":       c.UserID,
		"user_name":     c.UserRealName,
	}

	if c.Parameters != nil {
		fields["params"] = fmt.Sprintf("%v", c.Parameters)
	}

	return fields
}

// ParamAsString retrieves a string parameter by key from the Command's Parameters map.
func (c *Command) ParamAsString(key string) string {
	if c.Parameters == nil {
		return ""
	}

	if val, ok := c.Parameters[key]; ok {
		if valString, ok := val.(string); ok {
			return valString
		}
	}

	return ""
}

// ParamAsBool retrieves a boolean parameter by key from the Command's Parameters map.
func (c *Command) ParamAsBool(key string) bool {
	if c.Parameters == nil {
		return false
	}

	if val, ok := c.Parameters[key]; ok {
		if valBool, ok := val.(bool); ok {
			return valBool
		}
	}

	return false
}

// ParamAsInt retrieves an integer parameter by key from the Command's Parameters map.
func (c *Command) ParamAsInt(key string) int {
	if c.Parameters == nil {
		return 0
	}

	if val, ok := c.Parameters[key]; ok {
		if valInt, ok := val.(float64); ok {
			return int(valInt)
		}
	}

	return 0
}
