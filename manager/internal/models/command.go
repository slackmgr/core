package models

import (
	"encoding/json"
	"fmt"
	"time"

	commonlib "github.com/peteraglen/slack-manager-common"
)

type CommandAction string

const (
	CommandActionTerminateIssue     = CommandAction("terminate_issue")
	CommandActionResolveIssue       = CommandAction("resolve_issue")
	CommandActionUnresolveIssue     = CommandAction("unresolve_issue")
	CommandActionInvestigateIssue   = CommandAction("investigate_issue")
	CommandActionUninvestigateIssue = CommandAction("uninvestigate_issue")
	CommandActionMuteIssue          = CommandAction("mute_issue")
	CommandActionUnmuteIssue        = CommandAction("unmute_issue")
	CommandActionMoveIssue          = CommandAction("move_issue")
	CommandActionCreateIssue        = CommandAction("create_issue")
	CommandActionWebhook            = CommandAction("webhook")
)

type Command struct {
	IDValue                 string                 `json:"id,omitempty"`
	Timestamp               time.Time              `json:"timestamp,omitempty"`
	SlackChannelID          string                 `json:"slackChannelId,omitempty"`
	SlackPostID             string                 `json:"ts,omitempty"`
	Reaction                string                 `json:"reaction,omitempty"`
	UserID                  string                 `json:"userId,omitempty"`
	UserRealName            string                 `json:"userRealName,omitempty"`
	Action                  CommandAction          `json:"action,omitempty"`
	Parameters              map[string]interface{} `json:"parameters,omitempty"`
	WebhookParameters       *WebhookCommandParams  `json:"webhookParameters,omitempty"`
	IncludeArchivedIssues   bool                   `json:"includeArchivedIssues"`
	ExecuteWhenNoIssueFound bool                   `json:"executeWhenNoIssueFound"`

	message
}

type WebhookCommandParams struct {
	WebhookID     string              `json:"webhookId"`
	Input         map[string]string   `json:"input,omitempty"`
	CheckboxInput map[string][]string `json:"checkboxInput,omitempty"`
}

func NewCommandFromQueue(queueItem *commonlib.FifoQueueItem) (Message, error) {
	if len(queueItem.Body) == 0 {
		return nil, fmt.Errorf("alert body is empty")
	}

	var cmd Command

	if err := json.Unmarshal([]byte(queueItem.Body), &cmd); err != nil {
		return nil, fmt.Errorf("failed to unmarshal message body: %w", err)
	}

	cmd.message = newMessage(queueItem)

	return &cmd, nil
}

func NewCommand(slackChannelID, ts, reaction, userID, userRealName string, action CommandAction, parameters map[string]interface{}) *Command {
	timestamp := time.Now()
	id := fmt.Sprintf("%s-%s", slackChannelID, timestamp.Format(time.RFC3339Nano))

	return &Command{
		IDValue:        id,
		Timestamp:      timestamp,
		SlackChannelID: slackChannelID,
		SlackPostID:    ts,
		Reaction:       reaction,
		UserID:         userID,
		UserRealName:   userRealName,
		Action:         action,
		Parameters:     parameters,
	}
}

func (c *Command) ID() string {
	return c.IDValue
}

func (c *Command) DedupID() string {
	return fmt.Sprintf("command::%s::%s", c.SlackChannelID, c.Timestamp.Format(time.RFC3339Nano))
}

func (c *Command) LogFields() map[string]interface{} {
	if c == nil {
		return nil
	}

	fields := map[string]interface{}{
		"slack_channel_id": c.SlackChannelID,
		"slack_post_id":    c.SlackPostID,
		"reaction":         c.Reaction,
		"action":           c.Action,
		"user_id":          c.UserID,
		"user_name":        c.UserRealName,
	}

	if c.Parameters != nil {
		fields["params"] = fmt.Sprintf("%v", c.Parameters)
	}

	return fields
}

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
