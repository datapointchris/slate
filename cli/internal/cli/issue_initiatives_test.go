package cli

import (
	"strconv"
	"testing"
	"unicode/utf8"

	"github.com/datapointchris/slate/cli/internal/api"
)

func TestProgressBarShowsAnyStartAndHoldsBackFullUntilNothingIsOpen(t *testing.T) {
	cases := []struct {
		done, open, filled int
	}{
		{0, 0, 0},
		{0, 4, 0},
		{1, 24, 1},
		{4, 3, 6},
		{49, 1, 9},
		{7, 0, 10},
	}
	for _, c := range cases {
		if got := progressFilled(c.done, c.open); got != c.filled {
			t.Errorf("progressFilled(%d done, %d open) = %d, want %d", c.done, c.open, got, c.filled)
		}
	}
}

// A number read off `slate issues initiatives list` files an issue into that
// initiative, whether it is typed, typed with its #, or picked from the form.
func TestIssuesCreateTakesTheNumberTheInitiativeListPrints(t *testing.T) {
	validate := initiativeRef([]api.Initiative{{Number: 1501, Name: "Ship the tracker"}})
	for answer, want := range map[string]string{
		"1501":                  "1501",
		"#1501":                 "#1501",
		"1501 Ship the tracker": "1501",
		"ship the tracker":      "Ship the tracker",
	} {
		got, err := validate(answer)
		if err != nil || got != want {
			t.Errorf("initiativeRef(%q) = %q, %v; want %q", answer, got, err, want)
		}
	}
	if _, err := validate("Ship the router"); err == nil {
		t.Error("an unknown initiative name was accepted")
	}
}

// In a split pane the name keeps its floor and the repos give way, and a line
// with no room left for the name prints it whole rather than as nothing.
func TestASplitPaneKeepsTheInitiativeNameAndDropsTheRepos(t *testing.T) {
	const name = "Generate synthetic data from reviewable dataset profiles"
	const repos = "cli, datagen"
	cases := []struct {
		columns, used    int
		wantRepos        string
		nameMin, nameMax int
	}{
		{columns: 80, used: 27, wantRepos: repos, nameMin: nameFloor, nameMax: 80 - 27 - 2 - len(repos)},
		{columns: 40, used: 27, wantRepos: "", nameMin: 13, nameMax: 13},
		{columns: 40, used: 48, wantRepos: "", nameMin: utf8.RuneCountInString(name), nameMax: utf8.RuneCountInString(name)},
	}
	for _, c := range cases {
		t.Setenv("COLUMNS", strconv.Itoa(c.columns))
		gotName, gotRepos := fitNameAndRepos(name, repos, c.used)
		if n := utf8.RuneCountInString(gotName); gotRepos != c.wantRepos || n < c.nameMin || n > c.nameMax {
			t.Errorf("%d columns from %d: name %q (%d), repos %q; want %d-%d of the name and repos %q",
				c.columns, c.used, gotName, n, gotRepos, c.nameMin, c.nameMax, c.wantRepos)
		}
	}
}
