package client

import "time"

type WebhookCallback struct {
	ID            string                 `json:"id"`
	UserID        string                 `json:"userId"`
	UserRealName  string                 `json:"userRealName"`
	ChannelID     string                 `json:"channelId"`
	MessageID     string                 `json:"messageId"`
	Timestamp     time.Time              `json:"timestamp"`
	Input         map[string]string      `json:"input"`
	CheckboxInput map[string][]string    `json:"checkboxInput"`
	Payload       map[string]interface{} `json:"payload"`
}

func (w *WebhookCallback) GetPayloadValue(key string) interface{} {
	if w == nil || w.Payload == nil {
		return ""
	}

	if v, ok := w.Payload[key]; ok {
		return v
	}

	return nil
}

func (w *WebhookCallback) GetPayloadString(key string) string {
	if w == nil || w.Payload == nil {
		return ""
	}

	if s, ok := w.Payload[key]; ok {
		return s.(string)
	}

	return ""
}

func (w *WebhookCallback) GetPayloadInt(key string, defaultValue int) int {
	if w == nil || w.Payload == nil {
		return defaultValue
	}

	if s, ok := w.Payload[key]; ok {
		return s.(int)
	}

	return defaultValue
}

func (w *WebhookCallback) GetPayloadBool(key string, defaultValue bool) bool {
	if w == nil || w.Payload == nil {
		return defaultValue
	}

	if b, ok := w.Payload[key]; ok {
		return b.(bool)
	}

	return defaultValue
}

func (w *WebhookCallback) GetInputValue(key string) string {
	if w == nil || w.Input == nil {
		return ""
	}

	if s, ok := w.Input[key]; ok {
		return s
	}

	return ""
}

func (w *WebhookCallback) GetCheckboxInputSelectedValues(key string) []string {
	if w == nil || w.CheckboxInput == nil {
		return []string{}
	}

	if v, ok := w.CheckboxInput[key]; ok {
		return v
	}

	return []string{}
}
