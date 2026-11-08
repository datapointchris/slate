package cli

import (
	"cmp"
	"context"
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/datapointchris/gotermboard"

	"github.com/datapointchris/slate/cli/internal/api"
)

const tabRepos = "repos"

// repoSort is one order of the repos. A nil compare keeps the API's, by name.
type repoSort struct {
	label   string
	compare func(a, b *api.BoardRepo) int
}

var repoSorts = []repoSort{
	{label: "most open", compare: func(a, b *api.BoardRepo) int { return cmp.Compare(b.OpenCount, a.OpenCount) }},
	{label: "most ready", compare: func(a, b *api.BoardRepo) int { return cmp.Compare(b.States.Ready, a.States.Ready) }},
	{label: "longest wait", compare: func(a, b *api.BoardRepo) int { return oldestFirst(a.Ready, b.Ready) }},
	{label: "latest close", compare: func(a, b *api.BoardRepo) int {
		return latestFirst(a.Closes.LastClosedTS, b.Closes.LastClosedTS)
	}},
	{label: "name"},
}

// oldestFirst orders by each list's first ready issue, the one that has waited longest, and
// puts a repo with none ready last.
func oldestFirst(a, b []api.ReadyIssue) int {
	switch {
	case len(a) == 0 && len(b) == 0:
		return 0
	case len(a) == 0:
		return 1
	case len(b) == 0:
		return -1
	}
	return a[0].CreatedTS.Compare(b[0].CreatedTS)
}

// reposBoard is the repos tab. Its read runs off the program's goroutine and touches only
// its source and readFailure. The keys and Frame run on it.
type reposBoard struct {
	sort  int
	repos *gotermboard.Source[api.RepoBoard]
	// readFailure is the read's last failure, for --once and --json to exit on.
	readFailure error
}

func newReposBoard(client *api.Client) *reposBoard {
	zone := issueZone()
	b := &reposBoard{}
	b.repos = gotermboard.NewSource("repos", boardReadEvery, func(ctx context.Context) (api.RepoBoard, string, error) {
		read, err := client.ReadRepoBoard(ctx, zone)
		b.readFailure = err
		return read, "", err
	})
	return b
}

// Sources is gotermboard.Board's.
func (b *reposBoard) Sources() []gotermboard.Refresher { return []gotermboard.Refresher{b.repos} }

// Groupings is gotermboard.Board's.
func (b *reposBoard) Groupings() []string { return nil }

func (b *reposBoard) readOnce(ctx context.Context) error {
	gotermboard.RefreshAll(ctx, b.repos)
	return b.readFailure
}

// ordered is the repos in the order the sort leaves them.
func (b *reposBoard) ordered(read api.RepoBoard) []api.BoardRepo {
	repos := slices.Clone(read.Repos)
	if compare := repoSorts[b.sort].compare; compare != nil {
		slices.SortStableFunc(repos, func(x, y api.BoardRepo) int { return compare(&x, &y) })
	}
	return repos
}

// Actions is gotermboard.Keyed's.
func (b *reposBoard) Actions() []gotermboard.Action {
	return []gotermboard.Action{{
		Keys: []string{"s"}, Label: "cycle the order of the repos", Footer: "s sort",
		Run: func(m *gotermboard.Model) tea.Cmd {
			b.sort = (b.sort + 1) % len(repoSorts)
			m.Rebuild()
			return nil
		},
	}}
}

// reposSnapshot is what --json prints: the read the frame is drawn from, and the order the
// sort gives it.
type reposSnapshot struct {
	Tab   string               `json:"tab"`
	Sort  string               `json:"sort"`
	Order []string             `json:"order"`
	Reads []gotermboard.Status `json:"reads"`
	Board api.RepoBoard        `json:"board"`
}

func (b *reposBoard) snapshot() reposSnapshot {
	read := b.repos.Value()
	order := []string{}
	for _, repo := range b.ordered(read) {
		order = append(order, repoLabel(repo.Repo))
	}
	return reposSnapshot{
		Tab: tabRepos, Sort: repoSorts[b.sort].label, Order: order,
		Reads: []gotermboard.Status{b.repos.Status()}, Board: read,
	}
}
