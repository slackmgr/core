package config

import (
	"fmt"
	"regexp"
	"strings"
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

func (s *APISettings) InitAndValidate() error {
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
				return fmt.Errorf("rule[%d].equals[%d] is empty", i, j)
			}
			r.Equals[j] = s
		}

		for j, s := range r.HasPrefix {
			s = strings.ToLower(strings.TrimSpace(s))
			if s == "" {
				return fmt.Errorf("rule[%d].hasPrefix[%d] is empty", i, j)
			}
			r.HasPrefix[j] = s
		}

		for j, s := range r.MatchesRegex {
			s = strings.TrimSpace(s)
			if s == "" {
				return fmt.Errorf("rule[%d].matchesRegex[%d] is empty", i, j)
			}
			r.MatchesRegex[j] = s
		}

		r.Channel = strings.TrimSpace(r.Channel)

		if !r.MatchAll && allItemsAreEmpty(r.Equals) && allItemsAreEmpty(r.HasPrefix) && allItemsAreEmpty(r.MatchesRegex) {
			return fmt.Errorf("rule[%d] does not match anything", i)
		}

		for _, regexString := range r.MatchesRegex {
			regexString = strings.TrimSpace(regexString)

			// Ensure that the regex is case insensitive
			if !strings.HasPrefix(regexString, "(?i)") {
				regexString = "(?i)" + regexString
			}

			regex, err := regexp.Compile(regexString)
			if err != nil {
				return fmt.Errorf("failed to compile regex for rule[%d]: %w", i, err)
			}

			r.regex = append(r.regex, regex)
		}

		r.Channel = strings.TrimSpace(r.Channel)

		if r.Channel == "" {
			return fmt.Errorf("rule[%d].channel is empty", i)
		}

		if !slackChannelIDRegex.MatchString(r.Channel) {
			return fmt.Errorf("rule[%d].channel is not a valid Slack channel ID", i)
		}
	}

	s.ruleMatchCache = make(map[string]string)
	s.initialized = true

	return nil
}

func (s *APISettings) Match(key, alertType string) (string, bool) {
	cacheKey := key + "::" + alertType

	if channel, ok := s.ruleMatchCache[cacheKey]; ok {
		return channel, channel != ""
	}

	if rule := s.matchMappingRule(key, alertType); rule != nil {
		s.ruleMatchCache[cacheKey] = rule.Channel
		return rule.Channel, true
	}

	s.ruleMatchCache[cacheKey] = ""

	return "", false
}

func (s *APISettings) matchMappingRule(key, alertType string) *RoutingRule {
	if len(s.RoutingRules) == 0 {
		return nil
	}

	// Evaluate exact (equals) matches first
	if rule := s.findRuleWithEqualsMatch(key, alertType); rule != nil {
		return rule
	}

	// Then evaluate prefix matches
	if rule := s.findRuleWithPrefixMatch(key, alertType); rule != nil {
		return rule
	}

	// Then evalute regex matches
	if rule := s.findRuleWithRegexMatch(key, alertType); rule != nil {
		return rule
	}

	// Finally, try to find a match-all rule
	return s.findRuleWithMatchAll(alertType)
}

func (s *APISettings) findRuleWithEqualsMatch(key, alertType string) *RoutingRule {
	if key == "" {
		return nil
	}

	var emptyAlertTypeMatch *RoutingRule

	// If we find a rule that matches both key and alert type, return it immediately
	// If we find a rule that matches the key, but has an empty alert type, return it if no other rule matches both key and alert type
	for _, rule := range s.RoutingRules {
		if rule.matchEquals(key) {
			if alertType == rule.AlertType {
				return rule
			} else if rule.AlertType == "" && emptyAlertTypeMatch == nil {
				emptyAlertTypeMatch = rule
			}
		}
	}

	return emptyAlertTypeMatch
}

func (s *APISettings) findRuleWithPrefixMatch(key, alertType string) *RoutingRule {
	if key == "" {
		return nil
	}

	var emptyAlertTypeMatch *RoutingRule

	// If we find a rule that matches both key prefix and alert type, return it immediately
	// If we find a rule that matches the key prefix, but has an empty alert type, return it if no other rule matches both key prefix and alert type
	for _, rule := range s.RoutingRules {
		if rule.matchPrefix(key) {
			if alertType == rule.AlertType {
				return rule
			} else if rule.AlertType == "" && emptyAlertTypeMatch == nil {
				emptyAlertTypeMatch = rule
			}
		}
	}

	return emptyAlertTypeMatch
}

func (s *APISettings) findRuleWithRegexMatch(key, alertType string) *RoutingRule {
	if key == "" {
		return nil
	}

	var emptyAlertTypeMatch *RoutingRule

	// If we find a rule that regex matches both key and alert type, return it immediately
	// If we find a rule that regex matches the key, but has an empty alert type, return it if no other rule matches both key regex and alert type
	for _, rule := range s.RoutingRules {
		if rule.matchRegex(key) {
			if alertType == rule.AlertType {
				return rule
			} else if rule.AlertType == "" && emptyAlertTypeMatch == nil {
				emptyAlertTypeMatch = rule
			}
		}
	}

	return emptyAlertTypeMatch
}

func (s *APISettings) findRuleWithMatchAll(alertType string) *RoutingRule {
	// First try to find a match-all rule with the correct alert type
	for _, rule := range s.RoutingRules {
		if rule.MatchAll && alertType == rule.AlertType {
			return rule
		}
	}

	// Then try to find a match-all rule with an empty alert type
	for _, rule := range s.RoutingRules {
		if rule.MatchAll && rule.AlertType == "" {
			return rule
		}
	}

	return nil
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
