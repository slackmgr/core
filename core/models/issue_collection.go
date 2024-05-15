package models

import (
	"sync"
)

type IssueCollection struct {
	issuesByCorrelationID map[string]*Issue
	lock                  *sync.RWMutex
}

func NewIssueCollection(issues []*Issue) *IssueCollection {
	c := &IssueCollection{
		issuesByCorrelationID: make(map[string]*Issue),
		lock:                  &sync.RWMutex{},
	}

	for _, issue := range issues {
		c.issuesByCorrelationID[issue.CorrelationID] = issue
	}

	return c
}

func (c *IssueCollection) Count() int {
	c.lock.RLock()
	defer c.lock.RUnlock()

	return len(c.issuesByCorrelationID)
}

func (c *IssueCollection) All() []*Issue {
	c.lock.RLock()
	defer c.lock.RUnlock()

	issues := make([]*Issue, len(c.issuesByCorrelationID))
	i := 0

	for _, issue := range c.issuesByCorrelationID {
		issues[i] = issue
		i++
	}

	return issues
}

func (c *IssueCollection) Find(correlationID string) (*Issue, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()

	issue, found := c.issuesByCorrelationID[correlationID]

	return issue, found
}

func (c *IssueCollection) FindActiveIssueBySlackPost(slackPostID string) (*Issue, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()

	for _, issue := range c.issuesByCorrelationID {
		if issue.SlackPostID == slackPostID {
			return issue, true
		}
	}

	return nil, false
}

func (c *IssueCollection) Add(issue *Issue) {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.issuesByCorrelationID[issue.CorrelationID] = issue
}

func (c *IssueCollection) Remove(issue *Issue) {
	c.lock.Lock()
	defer c.lock.Unlock()

	delete(c.issuesByCorrelationID, issue.CorrelationID)
}

// UpdateChannelName updates all issues with the current channel name (if needed)
func (c *IssueCollection) UpdateChannelName(channelName string) []*Issue {
	c.lock.Lock()
	defer c.lock.Unlock()

	updated := []*Issue{}

	for _, issue := range c.issuesByCorrelationID {
		if issue.LastAlert == nil || issue.LastAlert.SlackChannelName == channelName {
			continue
		}

		issue.LastAlert.SlackChannelName = channelName

		updated = append(updated, issue)
	}

	return updated
}

func (c *IssueCollection) RegisterArchiving() []*Issue {
	c.lock.Lock()
	defer c.lock.Unlock()

	archivedIssues := []*Issue{}

	for _, issue := range c.issuesByCorrelationID {
		if issue.IsReadyForArchiving() {
			issue.RegisterArchiving()
			archivedIssues = append(archivedIssues, issue)
		}
	}

	return archivedIssues
}

func (c *IssueCollection) RegisterEscalation() []*EscalationResult {
	c.lock.Lock()
	defer c.lock.Unlock()

	results := []*EscalationResult{}

	for _, issue := range c.issuesByCorrelationID {
		results = append(results, issue.ApplyEscalationRules())
	}

	return results
}
