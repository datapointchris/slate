package cli

import (
	"fmt"
	"slices"

	"github.com/datapointchris/slate/cli/internal/api"
)

// grouped is what a row carries for the filters that narrow every kind: its
// number, the tags it reads as carrying, and the directions it is in.
type grouped struct {
	number     int
	tags       []string
	directions []api.DirectionSummary
}

// issueGrouped reads an issue's own tags and its initiative's, and its
// initiative's directions.
func issueGrouped(issue api.Issue) grouped {
	return grouped{number: issue.Number, tags: issue.EffectiveTags, directions: issue.Directions}
}

func ideaGrouped(idea api.Idea) grouped {
	return grouped{number: idea.Number, tags: idea.Tags, directions: idea.Directions}
}

func initiativeGrouped(initiative api.Initiative) grouped {
	return grouped{number: initiative.Number, tags: initiative.Tags, directions: initiative.Directions}
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
// outside the --direction the request narrowed to. An API from before either
// drops the filter and answers as though it held it, so a read sent with one
// passes through here.
func honoredGroupings[T any](tag, direction string, rows []T, of func(T) grouped) error {
	for _, row := range rows {
		g := of(row)
		if tag != "" && !slices.Contains(g.tags, tag) {
			return fmt.Errorf("the API ignored --tag: #%d came back without %s, so it predates tags", g.number, tag)
		}
		if direction != "" && !inDirection(direction, g.directions) {
			return fmt.Errorf("the API ignored --direction: #%d came back outside direction %s, so it predates directions",
				g.number, directionNamed(direction))
		}
	}
	return nil
}
