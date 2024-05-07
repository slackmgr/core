package issues

import (
	"sync"

	"github.com/peteraglen/slack-manager/lib/core/models"
)

type issueCollection struct {
	issuesByCorrelationID map[string]*models.Issue
	lock                  *sync.RWMutex
}

func newIssueCollection(issues []*models.Issue) *issueCollection {
	c := &issueCollection{
		issuesByCorrelationID: make(map[string]*models.Issue),
		lock:                  &sync.RWMutex{},
	}

	for _, issue := range issues {
		c.issuesByCorrelationID[issue.CorrelationID] = issue
	}

	return c
}

func (c *issueCollection) Count() int {
	c.lock.RLock()
	defer c.lock.RUnlock()

	return len(c.issuesByCorrelationID)
}

func (c *issueCollection) All() []*models.Issue {
	c.lock.RLock()
	defer c.lock.RUnlock()

	issues := make([]*models.Issue, len(c.issuesByCorrelationID))
	i := 0

	for _, issue := range c.issuesByCorrelationID {
		issues[i] = issue
		i++
	}

	return issues
}

func (c *issueCollection) Find(correlationID string) (*models.Issue, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()

	issue, found := c.issuesByCorrelationID[correlationID]

	return issue, found
}

func (c *issueCollection) FindActiveIssueBySlackPost(slackPostID string) (*models.Issue, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()

	for _, issue := range c.issuesByCorrelationID {
		if issue.SlackPostID == slackPostID {
			return issue, true
		}
	}

	return nil, false
}

func (c *issueCollection) Add(issue *models.Issue) {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.issuesByCorrelationID[issue.CorrelationID] = issue
}

func (c *issueCollection) Remove(issue *models.Issue) {
	c.lock.Lock()
	defer c.lock.Unlock()

	delete(c.issuesByCorrelationID, issue.CorrelationID)
}

// UpdateChannelName updates all issues with the current channel name (if needed)
func (c *issueCollection) UpdateChannelName(channelName string) []*models.Issue {
	c.lock.Lock()
	defer c.lock.Unlock()

	updated := []*models.Issue{}

	for _, issue := range c.issuesByCorrelationID {
		if issue.LastAlert == nil || issue.LastAlert.SlackChannelName == channelName {
			continue
		}

		issue.LastAlert.SlackChannelName = channelName

		updated = append(updated, issue)
	}

	return updated
}

func (c *issueCollection) RegisterArchiving() []*models.Issue {
	c.lock.Lock()
	defer c.lock.Unlock()

	archivedIssues := []*models.Issue{}

	for _, issue := range c.issuesByCorrelationID {
		if issue.IsReadyForArchiving() {
			issue.RegisterArchiving()
			archivedIssues = append(archivedIssues, issue)
		}
	}

	return archivedIssues
}

func (c *issueCollection) RegisterEscalation() []*models.EscalationResult {
	c.lock.Lock()
	defer c.lock.Unlock()

	results := []*models.EscalationResult{}

	for _, issue := range c.issuesByCorrelationID {
		results = append(results, issue.ApplyEscalationRules())
	}

	return results
}
