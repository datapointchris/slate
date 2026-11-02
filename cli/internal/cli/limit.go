package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
)

// limitUsage is the description every server-side --limit carries. It is shared
// text rather than a sentence written per command because a flag has to mean one
// thing everywhere in the binary: a caller who has read it once on `tasks list`
// has read it everywhere, and a divergence between two commands is invisible in
// help because both rows still read the same.
const limitUsage = "Maximum number of rows to return (default: every row)"

// limitValue parses --limit and refuses a negative, which is the floor a row
// count owes. pflag has no minimum, so the parser is this Set method: without
// it a -1 reaches a slice expression, and `items[:-1]` panics where a usage
// error was the answer.
//
// Type returns "int" so a --limit renders as one in help and reads back as one.
// limitFlag takes the value off this struct rather than through the name, so
// nothing depends on that string matching what pflag's own accessors expect.
type limitValue struct{ target *int }

func (v limitValue) String() string { return strconv.Itoa(*v.target) }

func (v limitValue) Type() string { return "int" }

func (v limitValue) Set(raw string) error {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Errorf("invalid limit %q: a row count is a whole number", raw)
	}
	if n < 0 {
		return fmt.Errorf("invalid limit %d: a row count is zero or more", n)
	}
	*v.target = n
	return nil
}

// addLimitFlag declares --limit/-n on a list read the API caps. The shorthand is
// part of the flag: every command listing a collection that grows outside the
// binary answers to the same two spellings.
//
// There is no default: an unset --limit sends no parameter, so the read is
// unbounded and the seeded zero never reaches a request. Every --limit in the
// CLI is declared here or by addCapFlag, so the floor and the usage text cannot
// diverge per command.
func addLimitFlag(cmd *cobra.Command, target *int) {
	*target = 0
	cmd.Flags().VarP(limitValue{target: target}, "limit", "n", limitUsage)
}

// addCapFlag declares --limit/-n on a command that caps what it prints rather
// than what it asks for, defaulting to fallback.
//
// `what` is a parameter because "rows" is wrong for these two: `overview` caps
// each section separately, so --limit 3 across seven sections prints up to 21
// rows. A shared sentence would be invisible in help and wrong in the answer.
// The default is seeded into target before registering, because pflag reads
// DefValue off the value's String method at that moment.
func addCapFlag(cmd *cobra.Command, target *int, fallback int, what string) {
	*target = fallback
	cmd.Flags().VarP(limitValue{target: target}, "limit", "n", what)
}

// noteCappedRows says on stderr when an addCapFlag cap cut rows from a table, so
// a short table is never read as the whole of what there is. It stays silent when
// everything fit, and for a zero cap, which asked for nothing.
func noteCappedRows(cmd *cobra.Command, shown int, total int) {
	if shown == 0 || shown >= total {
		return
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\n%d of %d shown: -n %d shows the rest\n", shown, total, total)
}

// limitFlag returns a *int for the --limit flag, or nil when it was not set (so
// the client omits the query param and the API returns everything).
//
// A limit is a row count and 0 is a count a caller can mean, so --limit 0 is a
// pointer to zero that reaches the server as `limit=0` and answers with
// nothing. Absence is what asks for every row, and it is the only thing that
// does.
//
// The value comes off the limitValue rather than through cmd.Flags().GetInt,
// whose error would have to be discarded and whose only guard is Type()
// returning the string pflag compares against. Under this reading a swallowed
// read is a zero, and a zero is now no rows rather than every row.
func limitFlag(cmd *cobra.Command) *int {
	if !cmd.Flags().Changed("limit") {
		return nil
	}
	declared, ok := cmd.Flags().Lookup("limit").Value.(limitValue)
	if !ok {
		panic("--limit was declared outside addLimitFlag/addCapFlag: TestEveryLimitFlagIsDeclaredByTheFactory pins this")
	}
	found := *declared.target
	return &found
}
