package models

import (
	"encoding/json"
	"fmt"
	"time"
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
	IDValue               string                 `json:"id,omitempty"`
	Timestamp             time.Time              `json:"timestamp,omitempty"`
	ChannelID             string                 `json:"channelId,omitempty"`
	SlackPostID           string                 `json:"ts,omitempty"`
	Reaction              string                 `json:"reaction,omitempty"`
	UserID                string                 `json:"userId,omitempty"`
	UserRealName          string                 `json:"userRealName,omitempty"`
	Action                CommandAction          `json:"action,omitempty"`
	Parameters            map[string]interface{} `json:"parameters,omitempty"`
	WebhookParameters     *WebhookCommandParams  `json:"webhookParameters,omitempty"`
	IncludeArchivedIssues bool                   `json:"includeArchivedIssues"`

	message
}

type WebhookCommandParams struct {
	WebhookID     string              `json:"webhookId"`
	Input         map[string]string   `json:"input,omitempty"`
	CheckboxInput map[string][]string `json:"checkboxInput,omitempty"`
}

func NewCommandFromSqsMsg(messageID, groupID, receiptHandle string, receiveTimestamp time.Time, visibilityTimeout time.Duration, body string) (Message, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("alert body is empty")
	}

	var cmd Command

	if err := json.Unmarshal([]byte(body), &cmd); err != nil {
		return nil, fmt.Errorf("failed to unmarshal SQS message body: %w", err)
	}

	cmd.message = newMessage(messageID, groupID, receiptHandle, receiveTimestamp, visibilityTimeout)

	return &cmd, nil
}

func NewCommand(channelID, ts, reaction, userID, userRealName string, action CommandAction, parameters map[string]interface{}) *Command {
	timestamp := time.Now()
	id := fmt.Sprintf("%s-%s", channelID, timestamp.Format(time.RFC3339Nano))

	return &Command{
		IDValue:      id,
		Timestamp:    timestamp,
		ChannelID:    channelID,
		SlackPostID:  ts,
		Reaction:     reaction,
		UserID:       userID,
		UserRealName: userRealName,
		Action:       action,
		Parameters:   parameters,
	}
}

func (c *Command) ID() string {
	return c.IDValue
}

func (c *Command) LogFields() map[string]interface{} {
	if c == nil {
		return nil
	}

	fields := map[string]interface{}{
		"slack_channel_id": c.ChannelID,
		"message_ts":       c.SlackPostID,
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
