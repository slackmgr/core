package client

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

var (
	// SlackChannelIDOrNameRegex matches valid Slack channel IDs and channel names. Names are converted to IDs by the API.
	SlackChannelIDOrNameRegex = regexp.MustCompile(`^[0-9a-zA-Z\-_]{1,80}$`)
	iconRegex                 = regexp.MustCompile(`^:[^:]{1,50}:$`)
	slackMentionRegex         = regexp.MustCompile(`^((<!here>)|(<!channel>)|(<@[^>\s]{1,100}>))$`)
	maxTimestampAge           = 7 * 24 * time.Hour
)

type Alerts struct {
	Alerts []*Alert `json:"alerts"`
}

// Alert represents a system alert/warning/info event
type Alert struct {
	Timestamp                 time.Time              `json:"timestamp"`
	CorrelationID             string                 `json:"correlationId"`
	Header                    string                 `json:"header"`
	HeaderWhenResolved        string                 `json:"headerWhenResolved"`
	Text                      string                 `json:"text"`
	TextWhenResolved          string                 `json:"textWhenResolved"`
	FallbackText              string                 `json:"fallbackText"`
	Author                    string                 `json:"author"`
	Host                      string                 `json:"host"`
	Footer                    string                 `json:"footer"`
	Link                      string                 `json:"link"`
	AutoResolveSeconds        int                    `json:"autoResolveSeconds"`
	AutoResolveAsInconclusive bool                   `json:"autoResolveAsInconclusive"`
	Severity                  AlertSeverity          `json:"severity"`
	SlackChannelID            string                 `json:"slackChannelId"`
	RouteKey                  string                 `json:"routeKey"`
	IssueFollowUpEnabled      bool                   `json:"issueFollowUpEnabled"`
	Username                  string                 `json:"username"`
	IconEmoji                 string                 `json:"iconEmoji"`
	Fields                    []*Field               `json:"fields"`
	NotificationDelaySeconds  int                    `json:"notificationDelaySeconds"`
	ArchivingDelaySeconds     int                    `json:"archivingDelaySeconds"`
	Escalation                []*Escalation          `json:"escalation"`
	IgnoreIfTextContains      []string               `json:"ignoreIfTextContains"`
	FailOnRateLimitError      bool                   `json:"failOnRateLimitError"`
	Webhooks                  []*Webhook             `json:"webhooks"`
	Metadata                  map[string]interface{} `json:"metadata"`
}

// Field represents a field in a Slack attachment. Keep the title and value short!
type Field struct {
	Title string `json:"title"`
	Value string `json:"value"`
}

type Escalation struct {
	Severity      AlertSeverity `json:"severity"`
	DelaySeconds  int           `json:"delaySeconds"`
	SlackMentions []string      `json:"slackMentions"`
	MoveToChannel string        `json:"moveToChannel"`
}

type Webhook struct {
	ID               string                   `json:"id"`
	URL              string                   `json:"url"`
	ConfirmationText string                   `json:"confirmationText"`
	ButtonText       string                   `json:"buttonText"`
	ButtonStyle      WebhookButtonStyle       `json:"buttonStyle"`
	AccessLevel      WebhookAccessLevel       `json:"accessLevel"`
	DisplayMode      WebhookDisplayMode       `json:"displayMode"`
	Payload          map[string]interface{}   `json:"payload"`
	PlainTextInput   []*WebhookPlainTextInput `json:"plainTextInput"`
	CheckboxInput    []*WebhookCheckboxInput  `json:"checkboxInput"`
}

type WebhookPlainTextInput struct {
	ID           string `json:"id"`
	Description  string `json:"description"`
	MinLength    int    `json:"minLength"`
	MaxLength    int    `json:"maxLength"`
	Multiline    bool   `json:"multiline"`
	InitialValue string `json:"initialValue"`
}

type WebhookCheckboxInput struct {
	ID      string                   `json:"id"`
	Label   string                   `json:"label"`
	Options []*WebhookCheckboxOption `json:"options"`
}

type WebhookCheckboxOption struct {
	Value    string `json:"value"`
	Text     string `json:"text"`
	Selected bool   `json:"selected"`
}

// NewPanicAlert returns an alert with the severity set to 'panic'
func NewPanicAlert() *Alert {
	return NewAlert(AlertPanic)
}

// NewErrorAlert returns an alert with the severity set to 'error'
func NewErrorAlert() *Alert {
	return NewAlert(AlertError)
}

// NewWarningAlert returns an alert with the severity set to 'warning'
func NewWarningAlert() *Alert {
	return NewAlert(AlertWarning)
}

// NewResolvedAlert returns an alert with the severity set to 'resolved'
func NewResolvedAlert() *Alert {
	return NewAlert(AlertResolved)
}

// NewInfoAlert returns an alert with the severity set to 'info'
func NewInfoAlert() *Alert {
	return NewAlert(AlertInfo)
}

// NewAlert returns an alert with the specified severity
func NewAlert(severity AlertSeverity) *Alert {
	return &Alert{
		Timestamp: time.Now().UTC(),
		Severity:  severity,
		Metadata:  make(map[string]interface{}),
	}
}

func (a *Alert) Clean() {
	if time.Since(a.Timestamp) > maxTimestampAge {
		a.Timestamp = time.Now()
	}

	a.SlackChannelID = strings.TrimSpace(a.SlackChannelID)
	a.RouteKey = strings.TrimSpace(a.RouteKey)
	a.Header = strings.ReplaceAll(strings.TrimSpace(a.Header), "\n", " ")
	a.HeaderWhenResolved = strings.ReplaceAll(strings.TrimSpace(a.HeaderWhenResolved), "\n", " ")
	a.Text = strings.TrimSpace(a.Text)
	a.TextWhenResolved = strings.TrimSpace(a.TextWhenResolved)
	a.FallbackText = strings.ReplaceAll(strings.TrimSpace(a.FallbackText), ":status:", "")
	a.FallbackText = strings.ReplaceAll(a.FallbackText, "\n", " ")
	a.CorrelationID = strings.TrimSpace(a.CorrelationID)
	a.Username = strings.TrimSpace(a.Username)
	a.Author = strings.TrimSpace(a.Author)
	a.Host = strings.TrimSpace(a.Host)
	a.Footer = strings.TrimSpace(a.Footer)
	a.IconEmoji = strings.TrimSpace(a.IconEmoji)
	a.Severity = AlertSeverity(strings.ToLower(strings.TrimSpace(string(a.Severity))))

	if len(a.FallbackText) > 150 {
		a.FallbackText = a.FallbackText[:147] + "..."
	}

	if a.Severity == "critical" {
		a.Severity = AlertError
	}

	if a.ArchivingDelaySeconds < 0 {
		a.ArchivingDelaySeconds = 0
	}

	if a.NotificationDelaySeconds < 0 {
		a.NotificationDelaySeconds = 0
	}

	// Max length is 150, see https://api.slack.com/reference/block-kit/blocks#header
	// We also need to leave some space for the :status: emoji to be replaced with something a bit longer
	if len(a.Header) > 130 {
		a.Header = strings.TrimSpace(a.Header[:127]) + "..."
	}

	if len(a.HeaderWhenResolved) > 140 {
		a.HeaderWhenResolved = strings.TrimSpace(a.HeaderWhenResolved[:137]) + "..."
	}

	a.Text = shortenAlertTextIfNeeded(a.Text)
	a.TextWhenResolved = shortenAlertTextIfNeeded(a.TextWhenResolved)

	if len(a.Author) > 100 {
		a.Author = strings.TrimSpace(a.Author[:97]) + "..."
	}

	if len(a.Host) > 100 {
		a.Host = strings.TrimSpace(a.Host[:97]) + "..."
	}

	if len(a.Username) > 100 {
		a.Username = strings.TrimSpace(a.Username[:97]) + "..."
	}

	if len(a.Footer) > 300 {
		a.Footer = strings.TrimSpace(a.Footer[:297]) + "..."
	}

	for _, field := range a.Fields {
		if len(field.Title) > 30 {
			field.Title = strings.TrimSpace(field.Title[:27]) + "..."
		}

		if len(field.Value) > 200 {
			field.Value = strings.TrimSpace(field.Value[:197]) + "..."
		}
	}

	for _, hook := range a.Webhooks {
		hook.ID = strings.TrimSpace(hook.ID)
		hook.ButtonText = strings.TrimSpace(hook.ButtonText)
		hook.URL = strings.TrimSpace(hook.URL)
		hook.ConfirmationText = strings.TrimSpace(hook.ConfirmationText)

		if hook.ButtonStyle == "default" {
			hook.ButtonStyle = ""
		}

		for _, input := range hook.PlainTextInput {
			input.ID = strings.TrimSpace(input.ID)
			input.Description = strings.TrimSpace(input.Description)
		}
	}

	if len(a.Escalation) > 0 {
		sort.Slice(a.Escalation, func(i, j int) bool {
			return a.Escalation[i].DelaySeconds < a.Escalation[j].DelaySeconds
		})

		for _, e := range a.Escalation {
			e.MoveToChannel = strings.TrimSpace(e.MoveToChannel)

			for i, mention := range e.SlackMentions {
				e.SlackMentions[i] = strings.TrimSpace(mention)
			}
		}
	}
}

// Validate returns an error if one or more of the required fields are empty or invalid
func (a *Alert) Validate() error {
	if a == nil {
		return errors.New("alert is nil")
	}

	if err := a.ValidateSlackChannelID(); err != nil {
		return err
	}

	if err := a.ValidateHeaderAndText(); err != nil {
		return err
	}

	if err := a.ValidateIcon(); err != nil {
		return err
	}

	if err := a.ValidateSeverity(); err != nil {
		return err
	}

	if err := a.ValidateCorrelationID(); err != nil {
		return err
	}

	if err := a.ValidateAutoResolve(); err != nil {
		return err
	}

	if err := a.ValidateFields(); err != nil {
		return err
	}

	if err := a.ValidateWebhooks(); err != nil {
		return err
	}

	if err := a.ValidateEscalation(); err != nil {
		return err
	}

	return a.ValidateIgnoreIfTextContains()
}

func (a *Alert) ValidateSlackChannelID() error {
	if a.SlackChannelID != "" {
		if !SlackChannelIDOrNameRegex.MatchString(a.SlackChannelID) {
			return fmt.Errorf("slackChannelId '%s' is invalid", a.SlackChannelID)
		}

		return nil
	}

	if a.RouteKey == "" {
		return errors.New("slackChannelId and routeKey cannot both be empty")
	}

	if len(a.RouteKey) > 1000 {
		return errors.New("routeKey is too long, expected length <=1000")
	}

	return nil
}

func (a *Alert) ValidateHeaderAndText() error {
	if a.Header == "" && a.Text == "" {
		return errors.New("header and text cannot both be empty")
	}

	return nil
}

func (a *Alert) ValidateIcon() error {
	if a.IconEmoji == "" {
		return nil
	}

	if !iconRegex.MatchString(a.IconEmoji) {
		return fmt.Errorf("iconEmoji '%s' is invalid", a.IconEmoji)
	}

	return nil
}

func (a *Alert) ValidateSeverity() error {
	if a.Severity == "" {
		return errors.New("severity cannot be empty")
	}

	if !SeverityIsValid(a.Severity) {
		return fmt.Errorf("severity '%s' is invalid, expected one of [%s]", a.Severity, strings.Join(ValidSeverities(), ", "))
	}

	return nil
}

func (a *Alert) ValidateCorrelationID() error {
	if a.CorrelationID == "" {
		return nil
	}

	if len(a.CorrelationID) > 500 {
		return errors.New("correlationId is too long, expected length <=500")
	}

	return nil
}

func (a *Alert) ValidateAutoResolve() error {
	if !a.IssueFollowUpEnabled {
		return nil
	}

	if a.AutoResolveSeconds < 30 {
		return fmt.Errorf("autoResolveSeconds %d is too low, expected value >=30", a.AutoResolveSeconds)
	}

	// 2 years
	if a.AutoResolveSeconds > 63113851 {
		return fmt.Errorf("autoResolveSeconds %d is too high, expected value <=63113851 (2 years)", a.AutoResolveSeconds)
	}

	return nil
}

func (a *Alert) ValidateIgnoreIfTextContains() error {
	if len(a.IgnoreIfTextContains) == 0 {
		return nil
	}

	for index, s := range a.IgnoreIfTextContains {
		if len(s) > 1000 {
			return fmt.Errorf("ignoreIfTextContains[%d] is too long, expected length <=1000", index)
		}
	}

	return nil
}

func (a *Alert) ValidateFields() error {
	if len(a.Fields) > 20 {
		return errors.New("too many fields, expected <=20")
	}

	return nil
}

func (a *Alert) ValidateWebhooks() error {
	if a.Webhooks == nil {
		return nil
	}

	if len(a.Webhooks) > 5 {
		return fmt.Errorf("too many webhooks, expected <=5")
	}

	webhookIDs := make(map[string]struct{})

	for index, hook := range a.Webhooks {
		if hook.ID == "" {
			return fmt.Errorf("webhook[%d].id is empty", index)
		}

		if _, ok := webhookIDs[hook.ID]; ok {
			return fmt.Errorf("webhook[%d].id must be unique", index)
		}

		webhookIDs[hook.ID] = struct{}{}

		if hook.URL == "" {
			return fmt.Errorf("webhook[%d].url is empty", index)
		}

		if len(hook.URL) > 1000 {
			return fmt.Errorf("webhook[%d].url is too long, expected length <=1000", index)
		}

		_, err := url.ParseRequestURI(hook.URL)
		if err != nil {
			return fmt.Errorf("webhook[%d].url '%s' is not a valid absolute URL", index, hook.URL)
		}

		if hook.ButtonText == "" {
			return fmt.Errorf("webhook[%d].buttonText is empty", index)
		}

		if len(hook.ButtonText) > 25 {
			return fmt.Errorf("webhook[%d].buttonText is too long, expected length <=25", index)
		}

		if len(hook.ConfirmationText) > 1000 {
			return fmt.Errorf("webhook[%d].displayText is too long, expected length <=1000", index)
		}

		if hook.ButtonStyle != "" && !WebhookButtonStyleIsValid(hook.ButtonStyle) {
			return fmt.Errorf("webhook[%d].buttonStyle '%s' is not valid, expected empty or one of [%s]", index, hook.ButtonStyle, strings.Join(ValidWebhookButtonStyles(), ", "))
		}

		if hook.AccessLevel != "" && !WebhookAccessLevelIsValid(hook.AccessLevel) {
			return fmt.Errorf("webhook[%d].accessLevel '%s' is not valid, expected empty or one of [%s]", index, hook.AccessLevel, strings.Join(ValidWebhookAccessLevels(), ", "))
		}

		if hook.DisplayMode != "" && !WebhookDisplayModeIsValid(hook.DisplayMode) {
			return fmt.Errorf("webhook[%d].displayMode '%s' is not valid, expected empty or one of [%s]", index, hook.DisplayMode, strings.Join(ValidWebhookDisplayModes(), ", "))
		}

		if len(hook.Payload) > 50 {
			return fmt.Errorf("webhook[%d].payload item count is too large, expected <=50", index)
		}

		if len(hook.PlainTextInput) > 10 {
			return fmt.Errorf("webhook[%d].plainTextInput item count is too large, expected <=10", index)
		}

		if len(hook.CheckboxInput) > 10 {
			return fmt.Errorf("webhook[%d].checkboxInput item count is too large, expected <=10", index)
		}

		inputIDs := make(map[string]struct{})

		for inputIndex, input := range hook.PlainTextInput {
			if input.ID == "" {
				return fmt.Errorf("webhook[%d].plainTextInput[%d].id is empty", index, inputIndex)
			}

			if _, ok := inputIDs[input.ID]; ok {
				return fmt.Errorf("webhook[%d].plainTextInput[%d].id must be unique among all inputs", index, inputIndex)
			}

			inputIDs[input.ID] = struct{}{}

			if len(input.ID) > 200 {
				return fmt.Errorf("webhook[%d].plainTextInput[%d].id is too long, expected <=200", index, inputIndex)
			}

			if len(input.Description) > 200 {
				return fmt.Errorf("webhook[%d].plainTextInput[%d].description is too long, expected <=200", index, inputIndex)
			}

			if input.MinLength < 0 {
				return fmt.Errorf("webhook[%d].plainTextInput[%d].minLength must be >=0", index, inputIndex)
			}

			if input.MinLength > 3000 {
				return fmt.Errorf("webhook[%d].plainTextInput[%d].minLength must be <=3000", index, inputIndex)
			}

			if input.MaxLength < 0 {
				return fmt.Errorf("webhook[%d].plainTextInput[%d].maxLength must be >=0", index, inputIndex)
			}

			if input.MaxLength > 3000 {
				return fmt.Errorf("webhook[%d].plainTextInput[%d].maxLength must be <=3000", index, inputIndex)
			}

			if input.MaxLength < input.MinLength {
				return fmt.Errorf("webhook[%d].plainTextInput[%d].maxLength cannot be smaller than minLength", index, inputIndex)
			}

			if len(input.InitialValue) > input.MaxLength {
				return fmt.Errorf("webhook[%d].plainTextInput[%d].initialValue cannot be longer than maxLength", index, inputIndex)
			}
		}

		for inputIndex, input := range hook.CheckboxInput {
			if input.ID == "" {
				return fmt.Errorf("webhook[%d].checkboxInput[%d].id is empty", index, inputIndex)
			}

			if _, ok := inputIDs[input.ID]; ok {
				return fmt.Errorf("webhook[%d].checkboxInput[%d].id must be unique among all inputs", index, inputIndex)
			}

			inputIDs[input.ID] = struct{}{}

			if len(input.ID) > 200 {
				return fmt.Errorf("webhook[%d].checkboxInput[%d].id is too long, expected <=200", index, inputIndex)
			}

			if len(input.Label) > 200 {
				return fmt.Errorf("webhook[%d].checkboxInput[%d].label is too long, expected <=200", index, inputIndex)
			}

			if len(input.Options) > 5 {
				return fmt.Errorf("webhook[%d].checkboxInput[%d].options item count is too large, expected <=5", index, inputIndex)
			}

			values := make(map[string]struct{})

			for optionIndex, option := range input.Options {
				if option.Value == "" {
					return fmt.Errorf("webhook[%d].checkboxInput[%d].options[%d].value is empty", index, inputIndex, optionIndex)
				}

				if _, ok := values[option.Value]; ok {
					return fmt.Errorf("webhook[%d].checkboxInput[%d].options[%d].value must be unique", index, inputIndex, optionIndex)
				}

				values[option.Value] = struct{}{}

				if len(option.Text) > 50 {
					return fmt.Errorf("webhook[%d].checkboxInput[%d].options[%d].text is too long, expected <=50", index, inputIndex, optionIndex)
				}
			}
		}
	}

	return nil
}

func (a *Alert) ValidateEscalation() error {
	if a.Escalation == nil {
		return nil
	}

	previousDelay := 0

	for index, e := range a.Escalation {
		if e.DelaySeconds < 30 {
			return fmt.Errorf("escalation[%d].delaySeconds '%d' is too low, expected value >=30", index, e.DelaySeconds)
		}

		if previousDelay > 0 && e.DelaySeconds-previousDelay < 30 {
			return fmt.Errorf("escalation[%d].delaySeconds '%d' is too small compared to previous escalation, expected diff >=30", index, e.DelaySeconds)
		}

		previousDelay = e.DelaySeconds

		if e.Severity != AlertPanic && e.Severity != AlertError && e.Severity != AlertWarning {
			return fmt.Errorf("escalation[%d].severity '%s' is invalid, expected one of [panic, error, warning]", index, e.Severity)
		}

		if len(e.SlackMentions) > 10 {
			return fmt.Errorf("escalation[%d].slackMentions have too many mentions, expected <=10", index)
		}

		for j, mention := range e.SlackMentions {
			if !slackMentionRegex.MatchString(mention) {
				return fmt.Errorf("escalation[%d].slackMentions[%d] '%s' is not valid", index, j, mention)
			}
		}

		if e.MoveToChannel != "" {
			if !SlackChannelIDOrNameRegex.MatchString(e.MoveToChannel) {
				return fmt.Errorf("escalation[%d].moveToChannel '%s' is invalid", index, e.MoveToChannel)
			}
		}
	}

	return nil
}

// EncryptPayload encrypts the existing payload and replaces it with an encrypted version
func (w *Webhook) EncryptPayload(key []byte) error {
	if w.Payload == nil || len(w.Payload) == 0 {
		return nil
	}

	if len(key) != 32 {
		return fmt.Errorf("encryption key length must be 32")
	}

	data, err := json.Marshal(w.Payload)
	if err != nil {
		return err
	}

	if len(data) > 2048 {
		return fmt.Errorf("length of JSON serialized webhook payload is %d, expected <= 2048", len(data))
	}

	encryptedData, err := Encrypt(key, data)
	if err != nil {
		return err
	}

	w.Payload = map[string]interface{}{
		"__encrypted_data": base64.StdEncoding.EncodeToString(encryptedData),
	}

	return nil
}

// DecryptPayload decrypts the encrypted payload (if any) and returns it, or nil if payload is empty.
// It does not change state.
func (w *Webhook) DecryptPayload(key []byte) (map[string]interface{}, error) {
	if w.Payload == nil || len(w.Payload) == 0 {
		return nil, nil
	}

	if len(key) != 32 {
		return nil, fmt.Errorf("encryption key length must be 32")
	}

	encryptedDataBase64, dataFound := w.Payload["__encrypted_data"]

	if !dataFound {
		return nil, nil
	}

	encryptedData, err := base64.StdEncoding.DecodeString(encryptedDataBase64.(string))
	if err != nil {
		return nil, err
	}

	data, err := Decrypt(key, encryptedData)
	if err != nil {
		return nil, err
	}

	originalPayload := make(map[string]interface{})

	if err := json.Unmarshal(data, &originalPayload); err != nil {
		return nil, err
	}

	return originalPayload, nil
}

func shortenAlertTextIfNeeded(text string) string {
	if len(text) <= 10000 {
		return text
	}

	endsWithCodeBlock := strings.HasSuffix(text, "```")
	shortened := strings.TrimSpace(text[:9997]) + "..."

	if endsWithCodeBlock {
		shortened += "```"
	}

	return shortened
}
