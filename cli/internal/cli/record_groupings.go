package cli

import (
	"fmt"
	"slices"

	"github.com/datapointchris/slate/cli/internal/api"
)

// grouped is what a row carries for the filters that narrow every kind: its
// number, the tags it reads as carrying, and the pursuits it is in.
type grouped struct {
	number   int
	tags     []string
	pursuits []api.PursuitSummary
}

// issueGrouped reads an issue's own tags and its initiative's, and its
// initiative's pursuits.
func issueGrouped(issue api.Issue) grouped {
	return grouped{number: issue.Number, tags: issue.EffectiveTags, pursuits: issue.Pursuits}
}

func ideaGrouped(idea api.Idea) grouped {
	return grouped{number: idea.Number, tags: idea.Tags, pursuits: idea.Pursuits}
}

func initiativeGrouped(initiative api.Initiative) grouped {
	return grouped{number: initiative.Number, tags: initiative.Tags, pursuits: initiative.Pursuits}
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

// honoredGroupings refuses a reply in which a row lacks the --tag or is
// outside the --pursuit the request narrowed to. An API from before either
// drops the filter and answers as though it held it, so a read sent with one
// passes through here.
func honoredGroupings[T any](tag, pursuit string, rows []T, of func(T) grouped) error {
	for _, row := range rows {
		g := of(row)
		if tag != "" && !slices.Contains(g.tags, tag) {
			return fmt.Errorf("the API ignored --tag: #%d came back without %s, so it predates tags", g.number, tag)
		}
		if pursuit != "" && !inPursuit(pursuit, g.pursuits) {
			return fmt.Errorf("the API ignored --pursuit: #%d came back outside pursuit %s, so it predates pursuits",
				g.number, pursuitNamed(pursuit))
		}
	}
	return nil
}
