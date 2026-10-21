package insights

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/slackmgr/core/internal/models"
	"github.com/slackmgr/types"
)

// CurrentStateQuery selects the current state of open issues.
type CurrentStateQuery struct {
	// ChannelIDs limits the result to these channels. Empty means all channels.
	ChannelIDs []string

	// StaleAfter is the time without new alerts after which an unresolved open issue is considered stale.
	// Zero means [DefaultStaleAfter].
	StaleAfter time.Duration

	// TopN is the length of the Oldest, Stale and MostAlerts lists. Zero means [DefaultTopN].
	TopN int

	// IncludeIssues includes all open issues in [CurrentState.Issues].
	IncludeIssues bool
}

// OpenIssue describes an open (non-archived) issue.
type OpenIssue struct {
	ID            string              `json:"id"`
	ChannelID     string              `json:"channelId"`
	ChannelName   string              `json:"channelName"`
	CorrelationID string              `json:"correlationId"`
	Header        string              `json:"header"`
	Severity      types.AlertSeverity `json:"severity"`
	RouteKey      string              `json:"routeKey,omitempty"`
	Host          string              `json:"host,omitempty"`
	Author        string              `json:"author,omitempty"`
	Created       time.Time           `json:"created"`
	LastAlertAt   time.Time           `json:"lastAlertAt"`
	AlertCount    int                 `json:"alertCount"`
	ReopenCount   int                 `json:"reopenCount"`
	FollowUp      bool                `json:"followUp"`

	// Resolved is true if the issue is resolved (by alert, user or auto-resolve) but not yet archived.
	Resolved   bool      `json:"resolved"`
	ResolvedBy string    `json:"resolvedBy,omitempty"`
	ResolveAt  time.Time `json:"resolveAt,omitzero"`
	ArchiveAt  time.Time `json:"archiveAt,omitzero"`

	Muted             bool      `json:"muted"`
	MutedBy           string    `json:"mutedBy,omitempty"`
	Investigated      bool      `json:"investigated"`
	InvestigatedBy    string    `json:"investigatedBy,omitempty"`
	InvestigatedSince time.Time `json:"investigatedSince,omitzero"`
	Escalated         bool      `json:"escalated"`
	Moved             bool      `json:"moved"`
	Terminated        bool      `json:"terminated"`

	// SlackPostID is the timestamp of the current Slack post, empty if none (e.g. a delayed notification).
	SlackPostID string `json:"slackPostId,omitempty"`
}

// StateCounts counts open issues by state.
type StateCounts struct {
	// Open is the number of open (non-archived) issues.
	Open int `json:"open"`

	// Alerting is the number of open issues that are neither resolved nor info, and BySeverity splits them by severity.
	Alerting   int                         `json:"alerting"`
	BySeverity map[types.AlertSeverity]int `json:"bySeverity"`

	Resolved     int `json:"resolved"`
	Muted        int `json:"muted"`
	Investigated int `json:"investigated"`
	Escalated    int `json:"escalated"`
	Moved        int `json:"moved"`

	// Stale is the number of alerting issues without new alerts for longer than StaleAfter.
	Stale int `json:"stale"`
}

// ChannelState holds the open issue counts of one channel.
type ChannelState struct {
	StateCounts

	ChannelID   string `json:"channelId"`
	ChannelName string `json:"channelName"`
}

// CurrentState is a snapshot of all open issues.
type CurrentState struct {
	GeneratedAt time.Time `json:"generatedAt"`

	// Totals counts all open issues in the selected channels.
	Totals StateCounts `json:"totals"`

	// Channels holds the counts per active channel (channels with open issues), ordered by alerting issues, then open issues.
	Channels []ChannelState `json:"channels"`

	// Oldest lists the oldest alerting issues, Stale the alerting issues longest without new alerts,
	// and MostAlerts the open issues with the most alerts.
	Oldest     []OpenIssue `json:"oldest"`
	Stale      []OpenIssue `json:"stale"`
	MostAlerts []OpenIssue `json:"mostAlerts"`

	// Issues holds all open issues, if requested with [CurrentStateQuery.IncludeIssues].
	Issues []OpenIssue `json:"issues,omitempty"`
}

// CurrentState returns a snapshot of the open issues. It works with an insights reader, or with any database
// set with [WithDB]. Results are cached for the configured cache TTL.
func (s *Service) CurrentState(ctx context.Context, q CurrentStateQuery) (*CurrentState, error) {
	if q.StaleAfter <= 0 {
		q.StaleAfter = DefaultStaleAfter
	}

	if q.TopN <= 0 {
		q.TopN = DefaultTopN
	}

	cacheKey := currentStateCacheKey(q)
	now := s.now()

	if cached := s.cachedState(cacheKey, now); cached != nil {
		return cached, nil
	}

	bodies, err := s.loadOpenIssues(ctx, q.ChannelIDs)
	if err != nil {
		return nil, err
	}

	issues := make([]OpenIssue, 0, len(bodies))

	for id, body := range bodies {
		issue, err := decodeOpenIssue(body, now)
		if err != nil {
			return nil, fmt.Errorf("failed to decode issue %s: %w", id, err)
		}

		if issue != nil {
			issues = append(issues, *issue)
		}
	}

	state := buildCurrentState(issues, q, now)
	s.storeState(cacheKey, state, now)

	return state, nil
}

func (s *Service) loadOpenIssues(ctx context.Context, channelIDs []string) (map[string]json.RawMessage, error) {
	if s.reader != nil {
		bodies, err := s.reader.LoadOpenIssues(ctx, channelIDs)
		if err != nil {
			return nil, fmt.Errorf("failed to load open issues: %w", err)
		}

		return bodies, nil
	}

	if s.db == nil {
		return nil, types.ErrInsightsNotSupported
	}

	channels := channelIDs

	if len(channels) == 0 {
		active, err := s.db.FindActiveChannels(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to find active channels: %w", err)
		}

		channels = active
	}

	bodies := map[string]json.RawMessage{}

	for _, channelID := range channels {
		channelBodies, err := s.db.LoadOpenIssuesInChannel(ctx, channelID)
		if err != nil {
			return nil, fmt.Errorf("failed to load open issues in channel %s: %w", channelID, err)
		}

		maps.Copy(bodies, channelBodies)
	}

	return bodies, nil
}

// decodeOpenIssue decodes issue JSON with the manager's own issue model, so that time-dependent state
// (e.g. auto-resolve) is evaluated exactly like the manager does. It returns nil for archived issues.
func decodeOpenIssue(body json.RawMessage, now time.Time) (*OpenIssue, error) {
	issue := &models.Issue{}

	if err := json.Unmarshal(body, issue); err != nil {
		return nil, err
	}

	if issue.LastAlert == nil {
		return nil, errors.New("issue has no last alert")
	}

	if issue.Archived {
		return nil, nil //nolint:nilnil // Archived issues are skipped
	}

	alert := issue.LastAlert
	resolved := issue.IsResolvedAt(now)

	o := &OpenIssue{
		ID:            issue.ID,
		ChannelID:     alert.SlackChannelID,
		ChannelName:   alert.SlackChannelName,
		CorrelationID: issue.CorrelationID,
		Header:        alert.Header,
		Severity:      alert.Severity,
		RouteKey:      alert.RouteKey,
		Host:          alert.Host,
		Author:        alert.Author,
		Created:       issue.Created,
		LastAlertAt:   issue.LastAlertReceived,
		AlertCount:    issue.AlertCount,
		ReopenCount:   issue.ReopenCount,
		FollowUp:      alert.IssueFollowUpEnabled,
		Resolved:      resolved,
		ArchiveAt:     issue.ArchiveTime,
		Muted:         issue.IsEmojiMuted,
		MutedBy:       issue.MutedByUser,
		Investigated:  issue.IsEmojiInvestigated,
		Escalated:     issue.IsEscalated,
		Moved:         issue.IsMoved,
		Terminated:    issue.IsEmojiTerminated,
		SlackPostID:   issue.SlackPostID,
	}

	if issue.IsEmojiInvestigated {
		o.InvestigatedBy = issue.InvestigatedByUser
		o.InvestigatedSince = issue.InvestigatedSince
	}

	if alert.IssueFollowUpEnabled {
		o.ResolveAt = issue.ResolveTime
	}

	if resolved {
		o.ResolvedBy = issue.Resolution()
	}

	return o, nil
}

func buildCurrentState(issues []OpenIssue, q CurrentStateQuery, now time.Time) *CurrentState {
	state := &CurrentState{
		GeneratedAt: now,
		Totals:      StateCounts{BySeverity: map[types.AlertSeverity]int{}},
		Channels:    []ChannelState{},
	}

	channels := map[string]*ChannelState{}

	var alerting, stale []OpenIssue

	for _, issue := range issues {
		ch, ok := channels[issue.ChannelID]
		if !ok {
			ch = &ChannelState{ChannelID: issue.ChannelID, StateCounts: StateCounts{BySeverity: map[types.AlertSeverity]int{}}}
			channels[issue.ChannelID] = ch
		}

		// Use the most recently updated channel name, since channels may be renamed.
		if issue.ChannelName != "" {
			ch.ChannelName = issue.ChannelName
		}

		isAlerting := !issue.Resolved && issue.Severity != types.AlertInfo
		isStale := isAlerting && now.Sub(issue.LastAlertAt) > q.StaleAfter

		for _, counts := range []*StateCounts{&state.Totals, &ch.StateCounts} {
			counts.add(issue, isAlerting, isStale)
		}

		if isAlerting {
			alerting = append(alerting, issue)
		}

		if isStale {
			stale = append(stale, issue)
		}
	}

	for _, ch := range channels {
		state.Channels = append(state.Channels, *ch)
	}

	sort.Slice(state.Channels, func(i, j int) bool {
		a, b := state.Channels[i], state.Channels[j]
		if a.Alerting != b.Alerting {
			return a.Alerting > b.Alerting
		}

		if a.Open != b.Open {
			return a.Open > b.Open
		}

		return a.ChannelID < b.ChannelID
	})

	state.Oldest = topIssues(alerting, q.TopN, func(a, b OpenIssue) bool { return a.Created.Before(b.Created) })
	state.Stale = topIssues(stale, q.TopN, func(a, b OpenIssue) bool { return a.LastAlertAt.Before(b.LastAlertAt) })
	state.MostAlerts = topIssues(issues, q.TopN, func(a, b OpenIssue) bool { return a.AlertCount > b.AlertCount })

	if q.IncludeIssues {
		state.Issues = topIssues(issues, len(issues), func(a, b OpenIssue) bool { return a.Created.Before(b.Created) })
	}

	return state
}

func (c *StateCounts) add(issue OpenIssue, isAlerting, isStale bool) {
	c.Open++

	if isAlerting {
		c.Alerting++
		c.BySeverity[issue.Severity]++
	}

	if issue.Resolved {
		c.Resolved++
	}

	if issue.Muted {
		c.Muted++
	}

	if issue.Investigated {
		c.Investigated++
	}

	if issue.Escalated {
		c.Escalated++
	}

	if issue.Moved {
		c.Moved++
	}

	if isStale {
		c.Stale++
	}
}

// topIssues returns up to n issues sorted with less (ties broken by ID), without modifying the input.
func topIssues(issues []OpenIssue, n int, less func(a, b OpenIssue) bool) []OpenIssue {
	sorted := slices.Clone(issues)

	sort.Slice(sorted, func(i, j int) bool {
		if less(sorted[i], sorted[j]) {
			return true
		}

		if less(sorted[j], sorted[i]) {
			return false
		}

		return sorted[i].ID < sorted[j].ID
	})

	if len(sorted) > n {
		sorted = sorted[:n]
	}

	if sorted == nil {
		return []OpenIssue{}
	}

	return sorted
}

func currentStateCacheKey(q CurrentStateQuery) string {
	channels := slices.Clone(q.ChannelIDs)
	slices.Sort(channels)

	return fmt.Sprintf("%s|%s|%d|%t", strings.Join(channels, ","), q.StaleAfter, q.TopN, q.IncludeIssues)
}

func (s *Service) cachedState(key string, now time.Time) *CurrentState {
	if s.cacheTTL <= 0 {
		return nil
	}

	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	entry, ok := s.cache[key]
	if !ok || !now.Before(entry.expires) {
		return nil
	}

	return entry.state
}

func (s *Service) storeState(key string, state *CurrentState, now time.Time) {
	if s.cacheTTL <= 0 {
		return
	}

	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	// Drop expired entries, so the cache cannot grow without bound.
	for k, entry := range s.cache {
		if !now.Before(entry.expires) {
			delete(s.cache, k)
		}
	}

	s.cache[key] = cachedState{state: state, expires: now.Add(s.cacheTTL)}
}
