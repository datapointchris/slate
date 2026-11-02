package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// DocumentKinds is what a document's --kind accepts.
var DocumentKinds = []string{"research", "design", "plan"}

// DocumentVersionSummary is one version of a document: who wrote it, the agent
// session that did, and the worklog that session was in.
type DocumentVersionSummary struct {
	Version   int       `json:"version"`
	WrittenBy string    `json:"written_by"`
	Session   *string   `json:"session"`
	Worklog   *string   `json:"worklog"`
	WrittenTS time.Time `json:"written_ts"`
	LineCount int       `json:"line_count"`
}

// DocumentVersion is one version with its body.
type DocumentVersion struct {
	DocumentVersionSummary
	Body string `json:"body"`
}

// DocumentSummary is a document as a record's read and a list carry it: its
// latest version and how many line comments it has.
type DocumentSummary struct {
	RecordNumber int                    `json:"record_number"`
	Name         string                 `json:"name"`
	Address      string                 `json:"address"`
	Kind         string                 `json:"kind"`
	CreatedTS    time.Time              `json:"created_ts"`
	Latest       DocumentVersionSummary `json:"latest"`
	CommentCount int                    `json:"comment_count"`
}

// DocumentVersionRef is a version a worklog wrote, named by its document's
// address.
type DocumentVersionRef struct {
	Address   string    `json:"address"`
	Kind      string    `json:"kind"`
	Version   int       `json:"version"`
	WrittenTS time.Time `json:"written_ts"`
}

// Document is one version of a document, with every version listed and every
// line comment placed on the version read.
type Document struct {
	Record    RecordSummary            `json:"record"`
	Name      string                   `json:"name"`
	Address   string                   `json:"address"`
	Kind      string                   `json:"kind"`
	CreatedTS time.Time                `json:"created_ts"`
	Version   DocumentVersion          `json:"version"`
	Versions  []DocumentVersionSummary `json:"versions"`
	Comments  []PlacedComment          `json:"comments"`
}

// DocumentWriteInput is the body for PUT /documents/{record}/{name}. Kind is
// needed to create the document, and must be its own when given after.
type DocumentWriteInput struct {
	Body    string  `json:"body"`
	Kind    *string `json:"kind,omitempty"`
	Session *string `json:"session,omitempty"`
	Host    *string `json:"host,omitempty"`
}

// DocumentWritten is the answer to writing a version.
type DocumentWritten struct {
	Document Document `json:"document"`
	WriteEffects
}

func documentPath(record, name string) string {
	return "/documents/" + url.PathEscape(record) + "/" + url.PathEscape(name)
}

// ListDocuments returns documents with their latest version, the most recently
// written first. A record narrows them to that record's.
func (c *Client) ListDocuments(ctx context.Context, record string, limit *int) ([]DocumentSummary, error) {
	query := url.Values{}
	if record != "" {
		query.Set("record", record)
	}
	applyLimit(query, limit)
	var documents []DocumentSummary
	if err := c.get(ctx, withQuery("/documents", query), &documents); err != nil {
		return nil, err
	}
	return documents, nil
}

// GetDocument returns one version of a document, the latest when version is
// nil, with its line comments placed on it.
func (c *Client) GetDocument(ctx context.Context, record, name string, version *int) (Document, error) {
	query := url.Values{}
	if version != nil {
		query.Set("version", strconv.Itoa(*version))
	}
	var document Document
	if err := c.get(ctx, withQuery(documentPath(record, name), query), &document); err != nil {
		return Document{}, err
	}
	return document, nil
}

// WriteDocument writes the document's next version, creating it on its first.
func (c *Client) WriteDocument(ctx context.Context, record, name string, in DocumentWriteInput) (DocumentWritten, error) {
	var written DocumentWritten
	if err := c.send(ctx, http.MethodPut, documentPath(record, name), in, &written); err != nil {
		return DocumentWritten{}, err
	}
	return written, nil
}
