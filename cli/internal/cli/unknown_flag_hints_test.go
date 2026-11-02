package cli

import (
	"strings"
	"testing"
)

// An unknown flag that is a value of one of the command's flags names that
// flag, and one asked for elsewhere names where, in place of the flag nearest
// in spelling: `--api` is two edits from `--all`.
func TestAnUnknownFlagNamesWhatItMeant(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"issues", "list", "--all"}, "all is a value of --status: slate issues list --status all"},
		{[]string{"initiatives", "list", "--someday"}, "slate initiatives list --status someday"},
		{[]string{"issues", "list", "--search", "cache"}, "slate search <words>"},
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
