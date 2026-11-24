package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Values of [APISettings.DefaultChannelAccess].
const (
	// ChannelAccessAllow lets principals without a matching access rule send alerts to all channels.
	ChannelAccessAllow = "allow"

	// ChannelAccessDeny rejects alerts from principals without a matching access rule.
	ChannelAccessDeny = "deny"
)

// accessRuleWildcard matches all principals or all channels in an [AccessRule].
const accessRuleWildcard = "*"

// AccessRule grants principals access to Slack channels, when the REST API has authentication enabled. Rules
// restrict the channels that authenticated principals may send alerts to, including the channels that
// escalations may move issues to (escalation moveToChannel). See [APISettings.ChannelAccessAllowed] for how rules
// are evaluated.
//
// # Example Configuration
//
//	defaultChannelAccess: allow
//	accessRules:
//	  - name: "alertmanager"
//	    principals: ["alertmanager-prod", "alertmanager-staging"]
//	    channels: ["C1234567890", "C2345678901"]
//
//	  - name: "team-a"
//	    principals: ["team-a-*"]
//	    channels: ["C3456789012"]
type AccessRule struct {
	// Name is a unique identifier for the rule, used in logging and debugging. Required and must be unique
	// across all access rules.
	Name string `json:"name" mapstructure:"name" yaml:"name"`

	// Description provides human-readable documentation for the rule. Optional.
	Description string `json:"description" mapstructure:"description" yaml:"description"`

	// Principals lists the principal IDs (e.g. API key IDs) that the rule applies to. An entry ending with "*"
	// matches all IDs with that prefix, and "*" alone matches all principals. Matching is case-sensitive.
	// At least one entry is required.
	Principals []string `json:"principals" mapstructure:"principals" yaml:"principals"`

	// Channels lists the Slack channel IDs (not names) that the principals may send alerts to. "*" allows all
	// channels. Channel IDs are case-insensitive. At least one entry is required.
	Channels []string `json:"channels" mapstructure:"channels" yaml:"channels"`
}

// ChannelAccessAllowed reports whether the principal with the given ID may send alerts to the given channel,
// according to the access rules and the default channel access:
//
//   - If one or more rules match the principal, the channel must be listed in (at least) one of them
//   - If no rule matches the principal, [APISettings.DefaultChannelAccess] decides
//
// The channel ID is matched case-insensitively. The settings must be initialized with
// [APISettings.InitAndValidate]; uninitialized settings allow no access (fail closed).
func (s *APISettings) ChannelAccessAllowed(principalID, channelID string) bool {
	if !s.initialized {
		return false
	}

	channelID = strings.ToUpper(strings.TrimSpace(channelID))
	matched := false

	for _, rule := range s.AccessRules {
		if !rule.matchesPrincipal(principalID) {
			continue
		}

		matched = true

		if slices.Contains(rule.Channels, accessRuleWildcard) || slices.Contains(rule.Channels, channelID) {
			return true
		}
	}

	if matched {
		return false
	}

	return s.DefaultChannelAccess == ChannelAccessAllow
}

func (r *AccessRule) matchesPrincipal(principalID string) bool {
	for _, p := range r.Principals {
		if prefix, ok := strings.CutSuffix(p, accessRuleWildcard); ok {
			if strings.HasPrefix(principalID, prefix) {
				return true
			}
		} else if p == principalID {
			return true
		}
	}

	return false
}

// initAccessRules normalizes and validates the default channel access and the access rules.
func (s *APISettings) initAccessRules() error {
	s.DefaultChannelAccess = strings.ToLower(strings.TrimSpace(s.DefaultChannelAccess))

	switch s.DefaultChannelAccess {
	case "":
		s.DefaultChannelAccess = ChannelAccessAllow
	case ChannelAccessAllow, ChannelAccessDeny:
	default:
		return fmt.Errorf("defaultChannelAccess must be %q or %q", ChannelAccessAllow, ChannelAccessDeny)
	}

	names := make(map[string]struct{}, len(s.AccessRules))

	for i, r := range s.AccessRules {
		if r == nil {
			return fmt.Errorf("accessRules[%d] cannot be null", i)
		}

		if err := r.init(); err != nil {
			return fmt.Errorf("accessRules[%d] is invalid: %w", i, err)
		}

		if _, ok := names[r.Name]; ok {
			return fmt.Errorf("accessRules[%d].name is not unique", i)
		}

		names[r.Name] = struct{}{}
	}

	return nil
}

func (r *AccessRule) init() error {
	r.Name = strings.TrimSpace(r.Name)

	if r.Name == "" {
		return errors.New("name cannot be empty")
	}

	r.Description = strings.TrimSpace(r.Description)

	if len(r.Principals) == 0 {
		return errors.New("principals cannot be empty")
	}

	for j, p := range r.Principals {
		p = strings.TrimSpace(p)

		if p == "" {
			return fmt.Errorf("principals[%d] cannot be empty", j)
		}

		if strings.Contains(strings.TrimSuffix(p, accessRuleWildcard), accessRuleWildcard) {
			return fmt.Errorf("principals[%d] %q can only have '*' at the end", j, p)
		}

		r.Principals[j] = p
	}

	if len(r.Channels) == 0 {
		return errors.New("channels cannot be empty")
	}

	for j, c := range r.Channels {
		c = strings.ToUpper(strings.TrimSpace(c))

		if c != accessRuleWildcard && !slackChannelIDRegex.MatchString(c) {
			return fmt.Errorf("channels[%d] %q is not a valid Slack channel ID or %q", j, c, accessRuleWildcard)
		}

		r.Channels[j] = c
	}

	return nil
}
