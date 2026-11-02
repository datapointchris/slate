package api

import "context"

// PriorityName names one value of the priority scale every kind shares.
type PriorityName struct {
	Value int    `json:"value"`
	Name  string `json:"name"`
}

// Vocabulary is every closed value a field of an issue or an initiative takes,
// in the order a picker lists them. Labels come with it because they are the
// one vocabulary that grows at runtime.
type Vocabulary struct {
	IssueStatuses      []string       `json:"issue_statuses"`
	IssueTypes         []string       `json:"issue_types"`
	Priorities         []PriorityName `json:"priorities"`
	InitiativeStatuses []string       `json:"initiative_statuses"`
	Labels             []IssueLabel   `json:"labels"`
}

// GetVocabulary returns the values each closed field accepts.
func (c *Client) GetVocabulary(ctx context.Context) (Vocabulary, error) {
	var vocabulary Vocabulary
	if err := c.get(ctx, "/vocabulary", &vocabulary); err != nil {
		return Vocabulary{}, err
	}
	return vocabulary, nil
}
