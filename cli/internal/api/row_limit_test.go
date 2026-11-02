package api

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func intptr(n int) *int { return &n }

func TestApplyLimit_SendsThePositiveCap(t *testing.T) {
	client, query := recordQuery(t, `[]`)
	if _, err := client.ListDocuments(context.Background(), "", intptr(5)); err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if *query != "limit=5" {
		t.Errorf("query = %q, want limit=5", *query)
	}
}

func TestApplyLimit_OmitsAnAbsentCap(t *testing.T) {
	client, query := recordQuery(t, `[]`)
	if _, err := client.ListDocuments(context.Background(), "", nil); err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if *query != "" {
		t.Errorf("query = %q, want no parameters at all", *query)
	}
}

// Zero is a row count a caller can mean, so it reaches the server as limit=0
// and the API answers with nothing. Omitting it is what asks for every row, and
// only an absent limit does that.
func TestApplyLimit_SendsAnExplicitZero(t *testing.T) {
	client, query := recordQuery(t, `[]`)
	if _, err := client.ListDocuments(context.Background(), "", intptr(0)); err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if *query != "limit=0" {
		t.Errorf("query = %q, want limit=0 so zero rows is what the caller gets", *query)
	}
}

// Every list read reaches the same helper, so the cap is spelled one way across
// the client rather than once per resource.
func TestApplyLimit_RidesEveryListRead(t *testing.T) {
	ctx := context.Background()
	calls := map[string]func(*Client) error{
		"ListInitiatives": func(c *Client) error {
			_, err := c.ListInitiatives(ctx, "", "", intptr(3))
			return err
		},
		"ListComments": func(c *Client) error {
			_, err := c.ListComments(ctx, "12", "", intptr(3))
			return err
		},
		"ListDocuments": func(c *Client) error {
			_, err := c.ListDocuments(ctx, "12", intptr(3))
			return err
		},
		"ListIssues": func(c *Client) error {
			_, err := c.ListIssues(ctx, IssueFilter{}, Window{}, "", intptr(3))
			return err
		},
		"ListReadyIssues": func(c *Client) error {
			_, err := c.ListReadyIssues(ctx, ReadyFilter{}, "", intptr(3))
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			client, query := recordQuery(t, `[]`)
			if err := call(client); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if !slices.Contains(strings.Split(*query, "&"), "limit=3") {
				t.Errorf("%s query = %q, want it to carry limit=3", name, *query)
			}
		})
	}
}
