package config

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"

	common "github.com/peteraglen/slack-manager-common"
)

const (
	keyIsEmpty = "key is empty"
)

var slackChannelIDRegex = regexp.MustCompile(`^[0-9a-zA-Z]{9,15}$`)

// APISettings contains runtime configuration for the REST API server.
//
// These settings control how incoming alerts are routed to Slack channels when alerts
// specify a route key rather than a direct channel ID. Unlike APIConfig (which requires
// a restart to change), APISettings can be updated at runtime via the Server's update method.
//
// # Alert Routing
//
// Alerts can specify their destination in two ways:
//  1. Direct channel ID: The alert's slackChannelID field contains a channel ID (e.g., "C1234567890")
//  2. Route key: The alert's routeKey field contains a logical identifier that is matched
//     against RoutingRules to determine the target channel
//
// When an alert uses a route key, the API evaluates RoutingRules in precedence order
// (exact match > prefix match > regex match > match-all) to find the target channel.
// See the Match method and RoutingRule documentation for details.
//
// # Immutability
//
// Settings passed to the API Server are cloned internally to prevent external modifications
// from affecting the running system. Once settings are passed to the Server, any subsequent
// changes to the original struct will have no effect. To update settings, create a new
// APISettings instance and pass it to the Server's update method.
type APISettings struct {
	// RoutingRules defines how alerts with route keys are mapped to Slack channels.
	// Rules are evaluated in precedence order: exact match, prefix match, regex match,
	// then match-all. Within each precedence level, rules with a matching AlertType
	// take priority over rules with no AlertType specified.
	//
	// If no rules match an alert's route key, the API returns an error. Consider adding
	// a catch-all rule (MatchAll: true) as the last rule to handle unmatched alerts.
	RoutingRules []*RoutingRule `json:"routingRules" yaml:"routingRules"`

	ruleMatchCache map[string]string
	cacheLock      sync.RWMutex
	initialized    bool
}

// RoutingRule defines a single rule for mapping alert route keys to Slack channels.
//
// # Matching Behavior
//
// A rule matches an alert's route key using one of these mechanisms (in precedence order):
//
//  1. Exact match (Equals): The route key exactly matches one of the Equals values
//  2. Prefix match (HasPrefix): The route key starts with one of the HasPrefix values
//  3. Regex match (MatchesRegex): The route key matches one of the regular expressions
//  4. Match-all (MatchAll): The rule matches any route key (used for catch-all rules)
//
// All matching is case-insensitive. A rule must have at least one matching mechanism
// configured (Equals, HasPrefix, MatchesRegex, or MatchAll).
//
// # AlertType Filtering
//
// If AlertType is set, the rule only matches alerts with that specific type. Rules
// with a matching AlertType take precedence over rules with no AlertType at the same
// precedence level. This allows routing different alert types to different channels
// even when they share the same route key.
//
// # Example Configuration
//
//	routingRules:
//	  - name: "security-alerts"
//	    alertType: "security"
//	    hasPrefix: ["prod-", "staging-"]
//	    channel: "C1234567890"  # #security-alerts
//
//	  - name: "prod-metrics"
//	    equals: ["prod-metrics", "production"]
//	    channel: "C2345678901"  # #prod-monitoring
//
//	  - name: "catch-all"
//	    matchAll: true
//	    channel: "C3456789012"  # #alerts-general
type RoutingRule struct {
	// Name is a unique identifier for the rule, used in logging and debugging.
	// Does not affect routing behavior. Required and must be unique across all rules.
	Name string `json:"name" yaml:"name"`

	// Description provides human-readable documentation for the rule.
	// Does not affect routing behavior. Optional.
	Description string `json:"description" yaml:"description"`

	// AlertType restricts this rule to alerts of a specific type. The value is
	// case-insensitive and matched against the alert's type field. Common values
	// include "security", "compliance", "metrics", "infrastructure".
	//
	// When multiple rules match a route key, rules with a matching AlertType take
	// precedence over rules with no AlertType. Leave empty to match all alert types.
	AlertType string `json:"alertType" yaml:"alertType"`

	// Equals lists exact route key values that this rule matches. Matching is
	// case-insensitive. The rule matches if the alert's route key equals any value
	// in this list. Exact matches have the highest precedence.
	Equals []string `json:"equals" yaml:"equals"`

	// HasPrefix lists route key prefixes that this rule matches. Matching is
	// case-insensitive. The rule matches if the alert's route key starts with any
	// value in this list. Prefix matches have second-highest precedence.
	HasPrefix []string `json:"hasPrefix" yaml:"hasPrefix"`

	// MatchesRegex lists regular expressions that this rule matches against route keys.
	// Patterns are automatically made case-insensitive ((?i) is prepended if not present).
	// The rule matches if the alert's route key matches any pattern. Regex matches
	// have third-highest precedence.
	MatchesRegex []string `json:"matchesRegex" yaml:"matchesRegex"`

	// MatchAll makes this rule match any route key, regardless of value. Used to
	// create catch-all rules that handle alerts not matched by more specific rules.
	// Match-all rules have the lowest precedence - they only match when no other
	// rule matches. Only one catch-all rule per AlertType is typically needed.
	MatchAll bool `json:"matchAll" yaml:"matchAll"`

	// Channel is the Slack channel ID where matching alerts are sent. Must be a valid
	// channel ID (e.g., "C1234567890"), not a channel name. Required.
	Channel string `json:"channel" yaml:"channel"`

	regex []*regexp.Regexp
}

// Clone creates a deep copy of the APISettings by marshaling to JSON and back.
// The returned clone is NOT initialized - InitAndValidate() must be called before use.
//
// This method is used internally by the API Server to ensure that external modifications
// to the original settings struct do not affect the running system.
//
// Only exported fields are copied. Unexported fields (compiled regexes, cache, mutex,
// initialized flag) are intentionally excluded since InitAndValidate() rebuilds them.
func (s *APISettings) Clone() (*APISettings, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal settings: %w", err)
	}

	clone := &APISettings{}
	if err := json.Unmarshal(data, clone); err != nil {
		return nil, fmt.Errorf("failed to unmarshal settings: %w", err)
	}

	return clone, nil
}

// InitAndValidate initializes internal data structures and validates all routing rules.
//
// This method performs the following operations:
//  1. Normalizes all string values (trims whitespace, converts to lowercase for matching)
//  2. Validates that each rule has a unique, non-empty name
//  3. Compiles regular expressions for MatchesRegex patterns
//  4. Validates that each rule has at least one matching mechanism configured
//  5. Validates that all channel IDs are valid Slack channel ID format
//  6. Initializes the route match cache for performance
//
// This method is idempotent - calling it multiple times on an already-initialized
// APISettings has no effect. The API Server calls this internally when settings
// are updated, so external callers typically don't need to call it directly.
//
// Returns an error describing the first validation failure encountered, or nil if valid.
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
			r.regex = append(r.regex, regex)
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

// Match finds the target Slack channel for an alert based on its route key and alert type.
//
// The method evaluates routing rules in precedence order to find the best match:
//
//  1. Exact match (Equals) - highest precedence
//  2. Prefix match (HasPrefix)
//  3. Regex match (MatchesRegex)
//  4. Match-all rule (MatchAll) - lowest precedence, used as catch-all
//
// Within each precedence level, rules with a matching AlertType take priority over rules
// with no AlertType configured. This allows different alert types to be routed to different
// channels even when they share the same route key.
//
// Results are cached in a thread-safe map for performance, as route matching occurs
// frequently during alert processing. Both the route key and alert type are normalized
// to lowercase before matching.
//
// Returns the target channel ID and true if a match is found, or ("", false) if no
// routing rule matches the given route key and alert type.
func (s *APISettings) Match(routeKey, alertType string, logger common.Logger) (string, bool) {
	routeKey = strings.ToLower(routeKey)
	alertType = strings.ToLower(alertType)
	cacheKey := routeKey + ":" + alertType

	// Check cache with read lock
	s.cacheLock.RLock()
	if channel, ok := s.ruleMatchCache[cacheKey]; ok {
		s.cacheLock.RUnlock()
		return channel, channel != ""
	}
	s.cacheLock.RUnlock()

	// Acquire write lock and double-check cache
	s.cacheLock.Lock()
	defer s.cacheLock.Unlock()

	// Double-check in case another goroutine added it while we waited for the lock
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
	return slices.Contains(r.Equals, key)
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
