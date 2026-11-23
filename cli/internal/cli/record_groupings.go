package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/datapointchris/slate/cli/internal/api"
)

// grouped is what a row carries for the filters that narrow every kind: its
// number, the tags it reads as carrying, the pursuits it is in, and its refs.
type grouped struct {
	number   int
	tags     []string
	pursuits []api.PursuitSummary
	refs     []api.RecordRef
}

// groupingFilter is what a read narrowed every kind to: a tag, a pursuit and
// a ref prefix, each empty where the read sent none.
type groupingFilter struct {
	tag, pursuit, ref string
}

// issueGrouped reads an issue's own tags and its initiative's, and its
// initiative's pursuits.
func issueGrouped(issue api.Issue) grouped {
	return grouped{number: issue.Number, tags: issue.EffectiveTags, pursuits: issue.Pursuits, refs: issue.Refs}
}

func ideaGrouped(idea api.Idea) grouped {
	return grouped{number: idea.Number, tags: idea.Tags, pursuits: idea.Pursuits, refs: idea.Refs}
}

func initiativeGrouped(initiative api.Initiative) grouped {
	return grouped{number: initiative.Number, tags: initiative.Tags, pursuits: initiative.Pursuits, refs: initiative.Refs}
}

func queueRowGrouped(row api.QueueRow) grouped {
	switch {
	case row.Issue != nil:
		return issueGrouped(*row.Issue)
	case row.Initiative != nil:
		return initiativeGrouped(*row.Initiative)
	case row.Idea != nil:
		return ideaGrouped(*row.Idea)
	}
	return grouped{number: row.Number}
}

// honoredGroupings refuses a reply in which a row lacks the --tag, is outside
// the --pursuit, or carries no ref starting with the --ref prefix that the
// request narrowed to. An API from before any of them drops the filter and
// answers as though it held it, so a read sent with one passes through here.
func honoredGroupings[T any](filter groupingFilter, rows []T, of func(T) grouped) error {
	for _, row := range rows {
		g := of(row)
		if filter.tag != "" && !slices.Contains(g.tags, filter.tag) {
			return fmt.Errorf("the API ignored --tag: #%d came back without %s, so it predates tags", g.number, filter.tag)
		}
		if filter.pursuit != "" && !inPursuit(filter.pursuit, g.pursuits) {
			return fmt.Errorf("the API ignored --pursuit: #%d came back outside pursuit %s, so it predates pursuits",
				g.number, pursuitNamed(filter.pursuit))
		}
		if filter.ref != "" && !slices.ContainsFunc(g.refs, func(ref api.RecordRef) bool { return strings.HasPrefix(ref.Ref, filter.ref) }) {
			return fmt.Errorf("the API ignored --ref: #%d came back carrying no ref that starts with %s, so it predates refs",
				g.number, filter.ref)
		}
	}
	return nil
}
