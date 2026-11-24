package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// DocumentKinds is what a document's --kind accepts.
var DocumentKinds = []string{"research", "design", "plan", "review", "retro"}

// DocumentVersionSummary is one version of a document: who wrote it, the agent
// session that did, the worklog that session was in, and the flow it wrote
// under.
type DocumentVersionSummary struct {
	Version   int       `json:"version"`
	WrittenBy string    `json:"written_by"`
	Session   *string   `json:"session"`
	Worklog   *string   `json:"worklog"`
	WrittenTS time.Time `json:"written_ts"`
	FlowID    *string   `json:"flow_id"`
	LineCount int       `json:"line_count"`
}

// DocumentVersion is one version with its body, and the body's lines as the
// API numbers them, which `slate comments add --line` takes.
type DocumentVersion struct {
	DocumentVersionSummary
	Body  string   `json:"body"`
	Lines []string `json:"lines"`
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

// ListedDocument is a document as a list carries it, naming its record.
type ListedDocument struct {
	DocumentSummary
	Record RecordSummary `json:"record"`
}

// DocumentFilter narrows a document list. Record names one record. RecordLimit
// keeps the documents on that many records, the newest written first, and nil
// leaves the API's default.
type DocumentFilter struct {
	Record      string
	RecordLimit *int
}

// DocumentList is one read of the document list. TotalRecords counts every
// record holding a matching document. It is nil where the API sent no count,
// which an API older than the record bound does.
type DocumentList struct {
	Documents    []ListedDocument
	TotalRecords *int
}

// totalRecordsHeader carries a document list's count of records.
const totalRecordsHeader = "X-Total-Records"

// ListDocuments returns documents with their record and latest version, the
// most recently written first.
func (c *Client) ListDocuments(ctx context.Context, filter DocumentFilter, limit *int) (DocumentList, error) {
	query := url.Values{}
	if filter.Record != "" {
		query.Set("record", filter.Record)
	}
	if filter.RecordLimit != nil {
		query.Set("record_limit", strconv.Itoa(*filter.RecordLimit))
	}
	applyLimit(query, limit)
	var list DocumentList
	header, err := c.getWithHeader(ctx, withQuery("/documents", query), &list.Documents)
	if err != nil {
		return DocumentList{}, err
	}
	if raw := header.Get(totalRecordsHeader); raw != "" {
		total, err := strconv.Atoi(raw)
		if err != nil {
			return DocumentList{}, fmt.Errorf("read the API's %s %q: %w", totalRecordsHeader, raw, err)
		}
		list.TotalRecords = &total
	}
	return list, nil
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
