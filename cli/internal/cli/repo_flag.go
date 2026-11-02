package cli

import (
	"strings"

	"github.com/datapointchris/goclikit"
	"github.com/spf13/cobra"

	"github.com/datapointchris/slate/cli/internal/repos"
)

// validateRepoFlag rejects a --repo the registry does not know, on reads and
// writes alike. On a write a typo'd tag is invisible rather than wrong: the
// record silently drops out of every repo view. On a read a name that selects
// nothing answers with an empty list and exit 0, which is also the answer for a
// repo with no work. A caller could not tell "nothing to do" from "no such repo".
func validateRepoFlag(cmd *cobra.Command, repo string) error {
	if !cmd.Flags().Changed("repo") {
		return nil
	}
	registry, err := repos.Load(repos.DefaultPath())
	if err != nil {
		return err
	}
	if err := registry.Validate(repo); err != nil {
		return goclikit.UsageError(err)
	}
	return nil
}

// repoFlagValue validates --repo and renders it as a read's filter. Absent is
// nil (no filter); present-but-empty is a pointer to "", which asks for the
// records that are not repo work at all.
func repoFlagValue(cmd *cobra.Command, repo string) (*string, error) {
	if !cmd.Flags().Changed("repo") {
		return nil, nil
	}
	if err := validateRepoFlag(cmd, repo); err != nil {
		return nil, err
	}
	return &repo, nil
}

// repoName accepts a registered repo name. A machine whose registry is missing
// accepts anything, which is the policy --repo already follows: an absent
// registry bans nothing.
func repoName(registry *repos.Registry) func(string) (string, error) {
	return func(answer string) (string, error) {
		if err := registry.Validate(answer); err != nil {
			return "", err
		}
		return answer, nil
	}
}

// repoList renders a set of repos. An em dash rather than a blank keeps the
// column readable where none is named.
func repoList(names []string) string {
	if len(names) == 0 {
		return "—"
	}
	return strings.Join(names, ",")
}
