package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// CommentLine is the document version and line a line comment was written on,
// and the line as it read there.
type CommentLine struct {
	Document string `json:"document"`
	Kind     string `json:"kind"`
	Version  int    `json:"version"`
	Line     int    `json:"line"`
	Text     string `json:"text"`
}

// Comment is a note on a record, or on one line of its document when On is
// set. WrittenBy is the caller's subject or client id, and nil on a comment
// nobody recorded the writer of.
type Comment struct {
	ID           int          `json:"id"`
	RecordNumber int          `json:"record_number"`
	Body         string       `json:"body"`
	WrittenBy    *string      `json:"written_by"`
	Session      *string      `json:"session"`
	Worklog      *string      `json:"worklog"`
	WrittenTS    time.Time    `json:"written_ts"`
	On           *CommentLine `json:"on"`
}

// PlacedComment is a line comment read with a version of its document.
// ShownLine is the line of that version it sits on, and nil when that version
// changed or removed the line it was written on.
type PlacedComment struct {
	Comment
	ShownLine *int `json:"shown_line"`
}

// CommentAddInput is the body for POST /comments. Document and Line together
// put it on a line, of the latest version unless Version names another.
type CommentAddInput struct {
	Record   string  `json:"record"`
	Body     string  `json:"body"`
	Document *string `json:"document,omitempty"`
	Version  *int    `json:"version,omitempty"`
	Line     *int    `json:"line,omitempty"`
	Session  *string `json:"session,omitempty"`
	Host     *string `json:"host,omitempty"`
}

// CommentWritten is the answer to adding a comment.
type CommentWritten struct {
	Comment Comment `json:"comment"`
	WriteEffects
}

// CommentDeleted is the answer to deleting a comment: the comment as it stood.
type CommentDeleted struct {
	Deleted Comment `json:"deleted"`
	WriteEffects
}

// ListComments returns a record's comments, oldest first, line comments on its
// documents included. A document narrows them to that document's.
func (c *Client) ListComments(ctx context.Context, record, document string, limit *int) ([]Comment, error) {
	query := url.Values{"record": {record}}
	if document != "" {
		query.Set("document", document)
	}
	applyLimit(query, limit)
	var comments []Comment
	if err := c.get(ctx, withQuery("/comments", query), &comments); err != nil {
		return nil, err
	}
	return comments, nil
}

// AddComment comments on a record or on one line of its document.
func (c *Client) AddComment(ctx context.Context, in CommentAddInput) (CommentWritten, error) {
	var written CommentWritten
	if err := c.send(ctx, http.MethodPost, "/comments", in, &written); err != nil {
		return CommentWritten{}, err
	}
	return written, nil
}

// DeleteComment deletes one comment by its id.
func (c *Client) DeleteComment(ctx context.Context, id int) (CommentDeleted, error) {
	var deleted CommentDeleted
	if err := c.send(ctx, http.MethodDelete, "/comments/"+strconv.Itoa(id), nil, &deleted); err != nil {
		return CommentDeleted{}, err
	}
	return deleted, nil
}
