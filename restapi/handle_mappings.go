package restapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// legacyAlertMappings is the /mappings response format. It matches the format of the
// legacy Slack Manager API, so that older clients can keep fetching routing rules.
type legacyAlertMappings struct {
	Rules []*legacyMappingRule `json:"rules"`
}

type legacyMappingRule struct {
	TeamName     string   `json:"teamName"`
	Description  string   `json:"description"`
	Equals       []string `json:"equals"`
	HasPrefix    []string `json:"hasPrefix"`
	MatchesRegex []string `json:"matchesRegex"`
	Channel      string   `json:"channel"`
	MatchAll     bool     `json:"matchAll"`
}

// handleMappings returns the routing rules in the legacy mappings format.
//
// Rules restricted to an alert type are omitted, since the legacy format has no alert type
// and such rules never match alerts without a type. Legacy clients require a non-empty
// description, so the rule name is used when the description is empty.
func (s *Server) handleMappings(c *gin.Context) {
	rules := s.apiSettings.Load().RoutingRules
	mappings := &legacyAlertMappings{Rules: make([]*legacyMappingRule, 0, len(rules))}

	for _, rule := range rules {
		if rule.AlertType != "" {
			continue
		}

		description := rule.Description
		if description == "" {
			description = rule.Name
		}

		mappings.Rules = append(mappings.Rules, &legacyMappingRule{
			TeamName:     rule.Name,
			Description:  description,
			Equals:       rule.Equals,
			HasPrefix:    rule.HasPrefix,
			MatchesRegex: rule.MatchesRegex,
			Channel:      rule.Channel,
			MatchAll:     rule.MatchAll,
		})
	}

	c.IndentedJSON(http.StatusOK, mappings)
}
