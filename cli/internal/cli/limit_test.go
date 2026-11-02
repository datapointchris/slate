package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// A capped table and a whole one look the same, so the cut is said out loud.
func TestNoteCappedRows_SaysHowManyWereCutAndHowToSeeThem(t *testing.T) {
	var stderr bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&stderr)

	noteCappedRows(cmd, 10, 18)

	if !strings.Contains(stderr.String(), "10 of 18 shown: -n 18 shows the rest") {
		t.Errorf("note = %q, want the counts and the flag that shows the rest", stderr.String())
	}
}

func TestNoteCappedRows_SilentWhenNothingWasCut(t *testing.T) {
	for _, c := range []struct {
		name         string
		shown, total int
	}{
		{"everything fit", 4, 4},
		{"a zero cap asked for nothing", 0, 18},
	} {
		t.Run(c.name, func(t *testing.T) {
			var stderr bytes.Buffer
			cmd := &cobra.Command{}
			cmd.SetErr(&stderr)

			noteCappedRows(cmd, c.shown, c.total)

			if stderr.String() != "" {
				t.Errorf("note = %q, want silence", stderr.String())
			}
		})
	}
}

// Every command whose verb is `list` reads a collection that grows outside the
// binary, so every one of them takes --limit/-n. Listing them here is what stops
// the next one being added without the flag — the tree walk below cannot, since a
// missing flag is a command the walk never sees.
func TestEveryListCommandTakesLimit(t *testing.T) {
	lists := [][]string{
		{"issues", "list"},
		{"initiatives", "list"},
	}
	for _, path := range lists {
		t.Run(strings.Join(path, " "), func(t *testing.T) {
			flag := findCommand(t, path...).Flags().Lookup("limit")
			if flag == nil {
				t.Fatalf("%s has no --limit", strings.Join(path, " "))
			}
			if flag.Shorthand != "n" {
				t.Errorf("--limit shorthand = %q, want n", flag.Shorthand)
			}
		})
	}
}

// An unset flag is nil so the client omits the parameter and every row comes
// back; an explicit zero is a pointer to zero, which is sent and answers with
// nothing. The two are opposite answers, so the distinction is the whole
// contract rather than an implementation detail.
func TestLimitFlag_DistinguishesUnsetFromExplicitZero(t *testing.T) {
	cmd := findCommand(t, "issues", "list")
	if got := limitFlag(cmd); got != nil {
		t.Errorf("limitFlag on an untouched command = %v, want nil", *got)
	}

	cmd = findCommand(t, "issues", "list")
	if err := cmd.Flags().Set("limit", "0"); err != nil {
		t.Fatalf("set limit: %v", err)
	}
	got := limitFlag(cmd)
	if got == nil {
		t.Fatal("limitFlag after --limit 0 = nil, want a pointer to 0")
	}
	if *got != 0 {
		t.Errorf("limitFlag after --limit 0 = %d, want 0", *got)
	}
}

func TestLimitFlag_CarriesAnExplicitCap(t *testing.T) {
	cmd := findCommand(t, "initiatives", "list")
	if err := cmd.Flags().Set("limit", "7"); err != nil {
		t.Fatalf("set limit: %v", err)
	}
	got := limitFlag(cmd)
	if got == nil || *got != 7 {
		t.Fatalf("limitFlag after --limit 7 = %v, want 7", got)
	}
}

// limitCommands is every command in the tree carrying --limit, with the path a
// failure names. Walked rather than listed: a command added with its own flag
// declaration is covered by nothing when the population is written by hand, and
// its absence from the list reads exactly like conformance.
func limitCommands(t *testing.T) map[string]*pflag.Flag {
	t.Helper()
	found := map[string]*pflag.Flag{}
	walkCommands(NewRootCommand(), func(cmd *cobra.Command) {
		if flag := cmd.Flags().Lookup("limit"); flag != nil {
			found[strings.TrimPrefix(cmd.CommandPath(), "slate ")] = flag
		}
	})
	if len(found) == 0 {
		t.Fatal("no command in the tree carries --limit, so every case below is vacuous")
	}
	return found
}

// Every --limit is declared by addLimitFlag or addCapFlag, which is what keeps
// the floor and the usage text from diverging per command. The factories are
// the only thing that builds a limitValue, so the type is the assertion.
func TestEveryLimitFlagIsDeclaredByTheFactory(t *testing.T) {
	for path, flag := range limitCommands(t) {
		t.Run(path, func(t *testing.T) {
			if flag.Shorthand != "n" {
				t.Errorf("%s --limit shorthand = %q, want n", path, flag.Shorthand)
			}
			if _, ok := flag.Value.(limitValue); !ok {
				t.Errorf("%s --limit is a %T, want limitValue — declare it with addLimitFlag or addCapFlag", path, flag.Value)
			}
		})
	}
}

// Every --limit refuses a negative, at the parser rather than in RunE. Past it,
// -1 reaches capItems as a slice bound and the API as a query parameter, and
// both answer at exit 0.
func TestEveryLimitFlagRefusesANegative(t *testing.T) {
	for path, flag := range limitCommands(t) {
		t.Run(path, func(t *testing.T) {
			err := flag.Value.Set("-1")
			if err == nil {
				t.Fatal("--limit -1 was accepted, want a usage error")
			}
			if !strings.Contains(err.Error(), "zero or more") {
				t.Errorf("error = %q, want it to say what a row count may be", err)
			}
		})
	}
}

// The commands that cap what they print carry a default; the reads the API
// bounds carry none, because an unset --limit sends no parameter at all.
func TestOnlyTheClientSideCapsCarryADefault(t *testing.T) {
	capped := map[string]string{"next": "10"}
	for path, flag := range limitCommands(t) {
		t.Run(path, func(t *testing.T) {
			want, isCapped := capped[path]
			if !isCapped {
				want = "0"
			}
			if flag.DefValue != want {
				t.Errorf("%s --limit default = %q, want %q", path, flag.DefValue, want)
			}
			if isCapped == strings.Contains(flag.Usage, "rows") {
				t.Errorf("%s --limit usage = %q; a per-section cap must not call its unit rows", path, flag.Usage)
			}
		})
	}
}
