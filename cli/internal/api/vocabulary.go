package api

import "context"

// PriorityName names one value of the priority scale every kind shares.
type PriorityName struct {
	Value int    `json:"value"`
	Name  string `json:"name"`
}

// WorklogStep is one step of a record's life, at its Position in the order a
// record goes through them.
type WorklogStep struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Position    int    `json:"position"`
}

// Vocabulary is every closed value a field takes, in the order a picker lists
// them. Labels and tags come with it because they are the vocabularies that
// grow at runtime.
type Vocabulary struct {
	IssueStatuses      []string       `json:"issue_statuses"`
	IssueTypes         []string       `json:"issue_types"`
	Priorities         []PriorityName `json:"priorities"`
	InitiativeStatuses []string       `json:"initiative_statuses"`
	PursuitStatuses    []string       `json:"pursuit_statuses"`
	DocumentKinds      []string       `json:"document_kinds"`
	WorklogSteps       []WorklogStep  `json:"worklog_steps"`
	QueueSteps         []string       `json:"queue_steps"`
	Labels             []IssueLabel   `json:"labels"`
	Tags               []Tag          `json:"tags"`
}

// GetVocabulary returns the values each closed field accepts.
func (c *Client) GetVocabulary(ctx context.Context) (Vocabulary, error) {
	var vocabulary Vocabulary
	if err := c.get(ctx, "/vocabulary", &vocabulary); err != nil {
		return Vocabulary{}, err
	}
	return vocabulary, nil
}
