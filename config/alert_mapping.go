package config

import (
	"fmt"
	"regexp"
	"strings"
)

var slackChannelIDRegex = regexp.MustCompile(`^[0-9a-zA-Z]{9,15}$`)

type AlertMapping struct {
	Rules       []*RoutingRule `json:"rules" yaml:"rules"`
	initialized bool
}

type RoutingRule struct {
	TeamName     string   `json:"teamName" yaml:"teamName"`
	Description  string   `json:"description" yaml:"description"`
	Equals       []string `json:"equals" yaml:"equals"`
	HasPrefix    []string `json:"hasPrefix" yaml:"hasPrefix"`
	MatchesRegex []string `json:"matchesRegex" yaml:"matchesRegex"`
	MatchAll     bool     `json:"matchAll" yaml:"matchAll"`
	Channel      string   `json:"channel" yaml:"channel"`

	regex []*regexp.Regexp
}

func (a *AlertMapping) InitAndValidate() error {
	if a.initialized {
		return nil
	}

	for i, r := range a.Rules {
		r.TeamName = strings.TrimSpace(r.TeamName)

		if r.TeamName == "" {
			return fmt.Errorf("rule[%d].teamName cannot be empty", i)
		}

		r.Description = strings.TrimSpace(r.Description)

		if r.Description == "" {
			return fmt.Errorf("rule[%d].description cannot be empty", i)
		}

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

		if !r.MatchAll && allItemsEmpty(r.Equals) && allItemsEmpty(r.HasPrefix) && allItemsEmpty(r.MatchesRegex) {
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

	a.initialized = true

	return nil
}

func (a *AlertMapping) Match(key string) (string, bool) {
	if rule := a.matchMappingRule(key); rule != nil {
		return rule.Channel, true
	}

	return "", false
}

func (a *AlertMapping) matchMappingRule(key string) *RoutingRule {
	if len(a.Rules) == 0 {
		return nil
	}

	key = strings.ToLower(strings.TrimSpace(key))

	if key == "" {
		return nil
	}

	// Evaluate exact (equals) matches first
	for _, rule := range a.Rules {
		if rule.matchEquals(key) {
			return rule
		}
	}

	// Then evaluate prefix matches
	for _, rule := range a.Rules {
		if rule.matchPrefix(key) {
			return rule
		}
	}

	// Then evalute regex matches
	for _, rule := range a.Rules {
		if rule.matchRegex(key) {
			return rule
		}
	}

	// Then try to find a match-all rule
	for _, rule := range a.Rules {
		if rule.MatchAll {
			return rule
		}
	}

	// Nothing found - return nil
	return nil
}

func (m *RoutingRule) matchEquals(key string) bool {
	for _, s := range m.Equals {
		if s != "" && key == s {
			return true
		}
	}

	return false
}

func (m *RoutingRule) matchPrefix(key string) bool {
	for _, s := range m.HasPrefix {
		if s != "" && strings.HasPrefix(key, s) {
			return true
		}
	}

	return false
}

func (m *RoutingRule) matchRegex(key string) bool {
	for _, regex := range m.regex {
		if regex != nil && regex.MatchString(key) {
			return true
		}
	}

	return false
}

func allItemsEmpty(items []string) bool {
	for _, s := range items {
		if s != "" {
			return false
		}
	}
	return true
}
