package config

import (
	"fmt"
	"regexp"
	"strings"

	common "github.com/peteraglen/slack-manager-common"
)

const (
	keyIsEmpty = "key is empty"
)

var slackChannelIDRegex = regexp.MustCompile(`^[0-9a-zA-Z]{9,15}$`)

// APISettings represents the dynamic settings for the API.
// They can updated for a running instance of the API, using api.Server.UpdateSettings(settings).
type APISettings struct {
	// RoutingRules is a list of rules that define how alerts are routed to Slack channels,
	// when alert route keys are used. They have no effect for alerts where the Slack channel ID is specified directly.
	// Refer to the Match method for details on how the rules are evaluated.
	RoutingRules []*RoutingRule `json:"routingRules" yaml:"routingRules"`

	ruleMatchCache map[string]string
	initialized    bool
}

// RoutingRule represents a single rule for routing alerts to Slack channels.
// Refer to the Match method for details on how the rules are evaluated.
type RoutingRule struct {
	// Name is a (short) unique name for the rule. It does not effect routing.
	// All rules must have a unique non-empty name.
	Name string `json:"name" yaml:"name"`

	// Description is a human-readable description of the rule. It does not effect routing.
	// This field is optional.
	Description string `json:"description" yaml:"description"`

	// AlertType is the case-insensitive type of alert that this rule matches.
	// This field is optional.
	// If empty, the rule matches all alert types, but rules with a non-empty AlertType take precedence.
	AlertType string `json:"alertType" yaml:"alertType"`

	// Equals is a list of case-insensitive exact matches for the alert route key.
	Equals []string `json:"equals" yaml:"equals"`

	// HasPrefix is a list of case-insensitive prefixes for the alert route key.
	HasPrefix []string `json:"hasPrefix" yaml:"hasPrefix"`

	// MatchesRegex is a list of case-insensitive regular expressions for the alert route key.
	// If a regex does not start with the case-insensitive flag (?i), it will be added automatically.
	MatchesRegex []string `json:"matchesRegex" yaml:"matchesRegex"`

	// MatchAll is a flag that indicates that this rule matches all alerts.
	// This field is typically used to create a catch-all (fallback) rule, to capture all alerts that do not match any other rule.
	MatchAll bool `json:"matchAll" yaml:"matchAll"`

	// Channel is the Slack channel ID to route the alert to (if the rule is a match), for example C1234567890.
	Channel string `json:"channel" yaml:"channel"`

	regex []*regexp.Regexp
}

// InitAndValidate initializes and validates the API settings.
// An error is returned if the settings are invalid.
// This function is called from inside the API, so it is not necessary to call it from the outside.
func (s *APISettings) InitAndValidate(logger common.Logger) error {
	if s.initialized {
		return nil
	}

	ruleNames := make(map[string]struct{})

	for i, r := range s.RoutingRules {
		r.Name = strings.TrimSpace(r.Name)

		if r.Name == "" {
			return fmt.Errorf("rule[%d].name cannot be empty", i)
		}

		if _, ok := ruleNames[r.Name]; ok {
			return fmt.Errorf("rule[%d].name is not unique", i)
		}

		ruleNames[r.Name] = struct{}{}

		r.Description = strings.TrimSpace(r.Description)

		r.AlertType = strings.ToLower(strings.TrimSpace(r.AlertType))

		for j, s := range r.Equals {
			s = strings.ToLower(strings.TrimSpace(s))

			if s == "" {
				return fmt.Errorf("rule[%d].equals[%d] cannot be empty", i, j)
			}

			r.Equals[j] = s
		}

		for j, s := range r.HasPrefix {
			s = strings.ToLower(strings.TrimSpace(s))

			if s == "" {
				return fmt.Errorf("rule[%d].hasPrefix[%d] cannot be empty", i, j)
			}

			r.HasPrefix[j] = s
		}

		r.regex = make([]*regexp.Regexp, 0, len(r.MatchesRegex))

		for j, s := range r.MatchesRegex {
			s = strings.TrimSpace(s)

			if s == "" {
				return fmt.Errorf("rule[%d].matchesRegex[%d] cannot be empty", i, j)
			}

			// Ensure that the regex is case insensitive
			if !strings.HasPrefix(s, "(?i)") {
				s = "(?i)" + s
			}

			regex, err := regexp.Compile(s)
			if err != nil {
				return fmt.Errorf("failed to compile regex for rule[%d]: %w", i, err)
			}

			r.MatchesRegex[j] = s
			r.regex[j] = regex
		}

		if !r.MatchAll && allItemsAreEmpty(r.Equals) && allItemsAreEmpty(r.HasPrefix) && allItemsAreEmpty(r.MatchesRegex) {
			return fmt.Errorf("rule[%d] does not match anything", i)
		}

		r.Channel = strings.TrimSpace(r.Channel)

		if r.Channel == "" {
			return fmt.Errorf("rule[%d].channel cannot be empty", i)
		}

		if !slackChannelIDRegex.MatchString(r.Channel) {
			return fmt.Errorf("rule[%d].channel is not a valid Slack channel ID", i)
		}
	}

	s.ruleMatchCache = make(map[string]string)
	s.initialized = true

	logger.Infof("API settings initialized with %d routing rules", len(s.RoutingRules))

	for i, rule := range s.RoutingRules {
		logger.Debugf("Routing rule #%d name:%s channel:%s equals:%v hasPrefix:%v regex:%v matchAll:%v", i, rule.Name, rule.Channel, rule.Equals, rule.HasPrefix, rule.MatchesRegex, rule.MatchAll)
	}

	return nil
}

// Match matches a route key and alert type to a Slack channel ID, based on the routing rules.
// If more than one rule matches, the most specific rule is returned.
// Specificity is determined by the type of match, in the following order:
//  1. Exact match
//  2. Prefix match
//  3. Regex match
//  4. Match-all rule
//
// Furthermore, rules with a non-empty matching AlertType take precedence over rules with an empty AlertType.
//
// If a match is found, the Slack channel ID is returned, along with a boolean indicating success.
// If no match is found, an empty string and false are returned.
func (s *APISettings) Match(routeKey, alertType string, logger common.Logger) (string, bool) {
	routeKey = strings.ToLower(routeKey)
	alertType = strings.ToLower(alertType)
	cacheKey := routeKey + "::" + alertType

	if channel, ok := s.ruleMatchCache[cacheKey]; ok {
		return channel, channel != ""
	}

	if rule, reason := s.matchMappingRule(routeKey, alertType); rule != nil {
		s.ruleMatchCache[cacheKey] = rule.Channel
		logger.Debugf("Matched route key '%s' and alert type '%s' to rule %s (channel %s): %s", routeKey, alertType, rule.Name, rule.Channel, reason)
		return rule.Channel, true
	}

	s.ruleMatchCache[cacheKey] = ""

	logger.Debugf("No matching route found for key '%s' and alert type '%s'", routeKey, alertType)

	return "", false
}

func (s *APISettings) matchMappingRule(key, alertType string) (*RoutingRule, string) {
	if len(s.RoutingRules) == 0 {
		return nil, "no rules defined"
	}

	// Evaluate exact (equals) matches first
	if rule, reason := s.findRuleWithEqualsMatch(key, alertType); rule != nil {
		return rule, reason
	}

	// Then evaluate prefix matches
	if rule, reason := s.findRuleWithPrefixMatch(key, alertType); rule != nil {
		return rule, reason
	}

	// Then evalute regex matches
	if rule, reason := s.findRuleWithRegexMatch(key, alertType); rule != nil {
		return rule, reason
	}

	// Finally, try to find a match-all rule
	return s.findRuleWithMatchAll(alertType)
}

func (s *APISettings) findRuleWithEqualsMatch(key, alertType string) (*RoutingRule, string) {
	if key == "" {
		return nil, keyIsEmpty
	}

	var emptyAlertTypeMatch *RoutingRule

	// If we find a rule that matches both key and alert type, return it immediately
	// If we find a rule that matches the key, but has an empty alert type, return it if no other rule matches both key and alert type
	for _, rule := range s.RoutingRules {
		if rule.matchEquals(key) {
			if alertType == rule.AlertType {
				return rule, "exact match for key, exact match for alert type"
			} else if rule.AlertType == "" && emptyAlertTypeMatch == nil {
				emptyAlertTypeMatch = rule
			}
		}
	}

	if emptyAlertTypeMatch != nil {
		return emptyAlertTypeMatch, "exact match for key, no match for alert type"
	}

	return nil, "no exact match found"
}

func (s *APISettings) findRuleWithPrefixMatch(key, alertType string) (*RoutingRule, string) {
	if key == "" {
		return nil, keyIsEmpty
	}

	var emptyAlertTypeMatch *RoutingRule

	// If we find a rule that matches both key prefix and alert type, return it immediately
	// If we find a rule that matches the key prefix, but has an empty alert type, return it if no other rule matches both key prefix and alert type
	for _, rule := range s.RoutingRules {
		if rule.matchPrefix(key) {
			if alertType == rule.AlertType {
				return rule, "prefix match for key, exact match for alert type"
			} else if rule.AlertType == "" && emptyAlertTypeMatch == nil {
				emptyAlertTypeMatch = rule
			}
		}
	}

	if emptyAlertTypeMatch != nil {
		return emptyAlertTypeMatch, "prefix match for key, no match for alert type"
	}

	return nil, "no prefix match found"
}

func (s *APISettings) findRuleWithRegexMatch(key, alertType string) (*RoutingRule, string) {
	if key == "" {
		return nil, keyIsEmpty
	}

	var emptyAlertTypeMatch *RoutingRule

	// If we find a rule that regex matches both key and alert type, return it immediately
	// If we find a rule that regex matches the key, but has an empty alert type, return it if no other rule matches both key regex and alert type
	for _, rule := range s.RoutingRules {
		if rule.matchRegex(key) {
			if alertType == rule.AlertType {
				return rule, "regex match for key, exact match for alert type"
			} else if rule.AlertType == "" && emptyAlertTypeMatch == nil {
				emptyAlertTypeMatch = rule
			}
		}
	}

	if emptyAlertTypeMatch != nil {
		return emptyAlertTypeMatch, "regex match for key, no match for alert type"
	}

	return nil, "no regex match found"
}

func (s *APISettings) findRuleWithMatchAll(alertType string) (*RoutingRule, string) {
	// First try to find a match-all rule with the correct alert type
	for _, rule := range s.RoutingRules {
		if rule.MatchAll && alertType == rule.AlertType {
			return rule, "match-all rule with exact alert type"
		}
	}

	// Then try to find a match-all rule with an empty alert type
	for _, rule := range s.RoutingRules {
		if rule.MatchAll && rule.AlertType == "" {
			return rule, "match-all rule with no alert type"
		}
	}

	return nil, "no match-all rule found"
}

func (r *RoutingRule) matchEquals(key string) bool {
	for _, s := range r.Equals {
		if key == s {
			return true
		}
	}

	return false
}

func (r *RoutingRule) matchPrefix(key string) bool {
	for _, s := range r.HasPrefix {
		if strings.HasPrefix(key, s) {
			return true
		}
	}

	return false
}

func (r *RoutingRule) matchRegex(key string) bool {
	for _, regex := range r.regex {
		if regex.MatchString(key) {
			return true
		}
	}

	return false
}

func allItemsAreEmpty(items []string) bool {
	for _, s := range items {
		if s != "" {
			return false
		}
	}
	return true
}
