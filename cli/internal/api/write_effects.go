package api

// WriteEffect is one change a write made beyond the request, or one condition
// it warns about. Code is stable for a client to branch on, Message is for a
// person, and Record names the record it concerns when there is one.
type WriteEffect struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Record  *int   `json:"record"`
}

// WriteEffects is what every write answers beside the record it wrote.
// Embedded in each *Written type, it flattens into the same JSON object.
type WriteEffects struct {
	SideEffects []WriteEffect `json:"side_effects"`
	Warnings    []WriteEffect `json:"warnings"`
}

// IssueWritten is the answer to a write on an issue.
type IssueWritten struct {
	Issue IssueDetail `json:"issue"`
	WriteEffects
}

// InitiativeWritten is the answer to a write on an initiative.
type InitiativeWritten struct {
	Initiative Initiative `json:"initiative"`
	WriteEffects
}

// RecordDeleted is the answer to deleting a record: the record as it stood
// before the delete took it.
type RecordDeleted struct {
	Deleted RecordSummary `json:"deleted"`
	WriteEffects
}

// IssueLabelWritten is the answer to a write on a label.
type IssueLabelWritten struct {
	Label IssueLabel `json:"label"`
	WriteEffects
}

// IssueLabelDeleted is the answer to deleting a label: the label as it stood,
// with each issue it came off named as a side effect.
type IssueLabelDeleted struct {
	Deleted IssueLabel `json:"deleted"`
	WriteEffects
}
