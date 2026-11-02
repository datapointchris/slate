package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/datapointchris/slate/cli/internal/api"
)

// historyShownByDefault is how many of a record's latest changes a show prints
// when --history is not given.
const historyShownByDefault = 20

// historyValueWidth is where a column's value is cut, so one change is one
// line per column however long a description or a body runs.
const historyValueWidth = 60

func addHistoryFlag(cmd *cobra.Command, target *int) {
	*target = historyShownByDefault
	cmd.Flags().Var(limitValue{target: target}, "history", "How many of the record's latest changes to print; 0 prints none")
}

// recordHistory is the latest changes a show prints, and whether the record
// has older ones it left out.
type recordHistory struct {
	changes []api.RecordChange
	older   bool
	shown   int
}

// MarshalJSON prints the changes alone, which is what a show's --json carries.
func (h recordHistory) MarshalJSON() ([]byte, error) {
	return json.Marshal(h.changes)
}

// ownRows names a record's own rows by table and key: its kind's row and its
// row in records. printHistory names every other row by its key.
func ownRows(kindTable, id string, number int) map[string]string {
	return map[string]string{kindTable: id, "records": strconv.Itoa(number)}
}

// readHistory reads the latest `shown` changes to record `number`. It asks for
// one more than it prints, so a history longer than the page is told apart
// from one that fills it exactly without counting every row.
func readHistory(cmd *cobra.Command, client *api.Client, number, shown int) (recordHistory, error) {
	history := recordHistory{changes: []api.RecordChange{}, shown: shown}
	if shown == 0 {
		return history, nil
	}
	asked := shown + 1
	changes, err := client.GetRecordHistory(cmd.Context(), number, &asked)
	if err != nil {
		return recordHistory{}, handleAPIError(err)
	}
	history.older = len(changes) > shown
	history.changes = changes[:min(len(changes), shown)]
	return history, nil
}

// noteOlderHistory says on stderr when older changes were left out, so a short
// history is never read as the whole of it.
func noteOlderHistory(cmd *cobra.Command, history recordHistory) {
	if history.older {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\nOlder changes not shown: --history %d shows more\n", history.shown*2)
	}
}

// printHistory prints each change newest first: when, who, and the row it
// wrote, then one line per column. The record's own rows are named by table
// alone, since their key is the record being shown.
func printHistory(out io.Writer, history recordHistory, own map[string]string, now time.Time) {
	if len(history.changes) == 0 {
		return
	}
	_, _ = fmt.Fprintf(out, "\nHistory, latest first (%d):\n", len(history.changes))
	for _, change := range history.changes {
		row := change.TableName
		if own[change.TableName] != change.RecordID {
			row += " " + change.RecordID
		}
		_, _ = fmt.Fprintf(out, "  %s  %s  %s %s\n", localClock(change.ChangedTS, now), orDash(strValue(change.Actor)), row, change.Operation)
		columns := make([]string, 0, len(change.Changes))
		for column := range change.Changes {
			columns = append(columns, column)
		}
		slices.Sort(columns)
		for _, column := range columns {
			if text, ok := columnChangeText(change.Changes[column]); ok {
				_, _ = fmt.Fprintf(out, "      %s: %s\n", column, text)
			}
		}
	}
}

// columnChangeText is an update's `old → new`, or the one value an insert or a
// delete wrote. A null that an insert or a delete carries says nothing, so it
// is left out.
func columnChangeText(change api.ColumnChange) (string, bool) {
	switch {
	case change.Old != nil && change.New != nil:
		return historyValue(change.Old) + " → " + historyValue(change.New), true
	case change.New != nil && !isJSONNull(change.New):
		return historyValue(change.New), true
	case change.Old != nil && !isJSONNull(change.Old):
		return historyValue(change.Old), true
	default:
		return "", false
	}
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// historyValue prints a string bare and anything else as its JSON, on one line
// and cut at historyValueWidth.
func historyValue(raw json.RawMessage) string {
	if isJSONNull(raw) {
		return orDash("")
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		text = string(raw)
	}
	first, _, cut := strings.Cut(strings.TrimRight(text, "\n"), "\n")
	if runes := []rune(first); len(runes) > historyValueWidth {
		first, cut = string(runes[:historyValueWidth]), true
	}
	if cut {
		first += "…"
	}
	return first
}
