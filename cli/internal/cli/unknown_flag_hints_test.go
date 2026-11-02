package cli

import (
	"strings"
	"testing"
)

// `slate issues list --all` names the flag `all` is a value of, and neither it
// nor `slate next --all` is answered with `--api`, a flag for choosing the
// server that is two edits from `--all`.
func TestAnUnknownFlagNamesWhatItMeant(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"issues", "list", "--all"}, "all is a value of --status: slate issues list --status all"},
		{[]string{"initiatives", "list", "--someday"}, "slate initiatives list --status someday"},
		{[]string{"next", "--all"}, "unknown flag: --all"},
	} {
		err := runLine(t, c.args...)
		if err == nil || !strings.Contains(err.Error(), c.want) || strings.Contains(err.Error(), "--api") {
			t.Errorf("%v answered %v, want it to name %q and not --api", c.args, err, c.want)
		}
		if got := exitCodeFor(err); got != 2 {
			t.Errorf("%v exit = %d, want 2", c.args, got)
		}
	}
}
