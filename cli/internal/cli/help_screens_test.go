package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/datapointchris/slate/cli/internal/api"
)

// A reader pipes help through `head`, so what to type has to be on the first
// screen. The root's opens with the grammar and the three commands that take
// no noun. A create's names its required field and the values of the fields
// that take a closed set, ahead of the flag list.
func TestHelpPutsWhatToTypeOnItsFirstLines(t *testing.T) {
	for _, c := range []struct {
		args  []string
		lines int
		want  []string
	}{
		{[]string{"--help"}, 8, []string{"The noun comes first and the verb last", "`slate next`", "`slate show <number>`", "`slate search <words>`"}},
		{[]string{"issues", "create", "--help"}, 4, []string{"--title is required", strings.Join(api.IssueTypes, ", ")}},
	} {
		root := NewRootCommand()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetArgs(c.args)
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		head := strings.Join(strings.SplitN(out.String(), "\n", c.lines+1)[:c.lines], "\n")
		for _, want := range c.want {
			if !strings.Contains(head, want) {
				t.Errorf("%v: the first %d lines\n%s\nwant %q", c.args, c.lines, head, want)
			}
		}
	}
}
