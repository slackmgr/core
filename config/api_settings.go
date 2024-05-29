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

type APISettings struct {
	RoutingRules []*RoutingRule `json:"routingRules" yaml:"routingRules"`

	ruleMatchCache map[string]string
	initialized    bool
}

type RoutingRule struct {
	TeamName     string   `json:"teamName"     yaml:"teamName"`
	Description  string   `json:"description"  yaml:"description"`
	AlertType    string   `json:"alertType"    yaml:"alertType"`
	Equals       []string `json:"equals"       yaml:"equals"`
	HasPrefix    []string `json:"hasPrefix"    yaml:"hasPrefix"`
	MatchesRegex []string `json:"matchesRegex" yaml:"matchesRegex"`
	MatchAll     bool     `json:"matchAll"     yaml:"matchAll"`
	Channel      string   `json:"channel"      yaml:"channel"`

	regex []*regexp.Regexp
}

func (s *APISettings) InitAndValidate(logger common.Logger) error {
	if s.initialized {
		return nil
	}

	for i, r := range s.RoutingRules {
		r.TeamName = strings.TrimSpace(r.TeamName)

		if r.TeamName == "" {
			return fmt.Errorf("rule[%d].teamName cannot be empty", i)
		}

		r.Description = strings.TrimSpace(r.Description)

		if r.Description == "" {
			return fmt.Errorf("rule[%d].description cannot be empty", i)
		}

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
		logger.Debugf("Routing rule #%d team:%s channel:%s equals:%v hasPrefix:%v regex:%v matchAll:%v", i, rule.TeamName, rule.Channel, rule.Equals, rule.HasPrefix, rule.MatchesRegex, rule.MatchAll)
	}

	return nil
}

func (s *APISettings) Match(key, alertType string, logger common.Logger) (string, bool) {
	cacheKey := key + "::" + alertType

	if channel, ok := s.ruleMatchCache[cacheKey]; ok {
		return channel, channel != ""
	}

	if rule, reason := s.matchMappingRule(key, alertType); rule != nil {
		s.ruleMatchCache[cacheKey] = rule.Channel
		logger.Debugf("Matched route key '%s' and alert type '%s' to rule %s (channel %s): %s", key, alertType, rule.TeamName, rule.Channel, reason)
		return rule.Channel, true
	}

	s.ruleMatchCache[cacheKey] = ""

	logger.Debugf("No matching route found for key '%s' and alert type '%s'", key, alertType)

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

	return emptyAlertTypeMatch, "exact match for key, no match for alert type"
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

	return emptyAlertTypeMatch, "prefix match for key, no match for alert type"
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

	return emptyAlertTypeMatch, "regex match for key, no match for alert type"
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
		if s != "" && key == s {
			return true
		}
	}

	return false
}

func (r *RoutingRule) matchPrefix(key string) bool {
	for _, s := range r.HasPrefix {
		if s != "" && strings.HasPrefix(key, s) {
			return true
		}
	}

	return false
}

func (r *RoutingRule) matchRegex(key string) bool {
	for _, regex := range r.regex {
		if regex != nil && regex.MatchString(key) {
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
