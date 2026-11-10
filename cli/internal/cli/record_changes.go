package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/datapointchris/slate/cli/internal/api"
)

// changesShownByDefault is how many of a record's latest changes a show
// prints when --changes is not given.
const changesShownByDefault = 20

// changeValueWidth is where a column's value is cut, so one change is one
// line per column however long a description or a body runs.
const changeValueWidth = 60

func addChangesFlag(cmd *cobra.Command, target *int) {
	*target = changesShownByDefault
	cmd.Flags().Var(limitValue{target: target}, "changes",
		"How many of the record's latest changes to print; 0 prints none, and --json carries none unless this is set")
}

// recordChanges is what a show read of a record's latest changes: the rows,
// whether older ones exist, and why the read failed when it did. --json
// carries it as `changes`, and leaves the key out when the show read none.
// --json reads changes only when --changes is set, and then a failure fails
// the command, so Error never reaches the JSON.
type recordChanges struct {
	Rows  []api.RecordChange `json:"rows"`
	Older bool               `json:"older"`
	Error string             `json:"-"`
}

// readChanges reads the latest `count` changes to record `number`, unless the
// show reads none: --changes 0, or --json with --changes left unset. It asks
// for one more than it keeps, so more changes are told apart from exactly
// `count` without counting every row.
//
// The changes are one section of a show, so a failed read leaves the record
// shown and says why. Only a --changes the caller set fails the command,
// because then the changes are what was asked for.
func readChanges(cmd *cobra.Command, client *api.Client, number, count int, asJSON bool) (*recordChanges, error) {
	asked := cmd.Flags().Changed("changes")
	if count == 0 || (asJSON && !asked) {
		return nil, nil
	}
	limit := count + 1
	rows, err := client.GetRecordChanges(cmd.Context(), number, &limit)
	if err != nil {
		failure := changesReadFailure(err)
		if asked {
			return nil, failure
		}
		return &recordChanges{Rows: []api.RecordChange{}, Error: failure.Error()}, nil
	}
	return &recordChanges{Rows: rows[:min(len(rows), count)], Older: len(rows) > count}, nil
}

// changesReadFailure names the read that failed. The record was read a moment
// before, so a 404 here is an API older than the route, never a missing record.
func changesReadFailure(err error) error {
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.NotFound() {
		return errors.New("this API serves no record changes, so it predates them; --changes 0 skips the read")
	}
	return fmt.Errorf("the record's changes were not read: %w", handleAPIError(err))
}

// noteChanges says on stderr what the printed changes leave out: a read that
// failed, or older changes past the ones shown.
func noteChanges(cmd *cobra.Command, changes *recordChanges) {
	switch {
	case changes == nil:
	case changes.Error != "":
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %s\n", changes.Error)
	case changes.Older:
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\nOlder changes not shown: --changes %d shows more\n", len(changes.Rows)*2)
	}
}

// printChanges prints each change newest first: when, who, and the row it
// wrote, then one line per column. A write to the record's own row is named
// by table alone, since its key is the record being shown.
func printChanges(out io.Writer, changes *recordChanges, now time.Time) {
	if changes == nil || len(changes.Rows) == 0 {
		return
	}
	_, _ = fmt.Fprintf(out, "\nChanges, latest first (%d):\n", len(changes.Rows))
	for _, change := range changes.Rows {
		row := change.TableName
		if !change.IsOwnRow {
			row += " " + change.RecordID
		}
		_, _ = fmt.Fprintf(out, "  %s  %s  %s %s\n", localClock(change.ChangedTS, now), orDash(strValue(change.Actor)), row, change.Operation)
		columns := make([]string, 0, len(change.Changes))
		for column := range change.Changes {
			columns = append(columns, column)
		}
		slices.Sort(columns)
		for _, column := range columns {
			if text, ok := columnChangeText(change.Changes[column], change.ReferencedNames[column]); ok {
				_, _ = fmt.Fprintf(out, "      %s: %s\n", column, text)
			}
		}
	}
}

// columnChangeText is an update's `old → new`, or the one value an insert or a
// delete wrote, each followed by the name of the row it keys where the API
// gave one. A null that an insert or a delete carries says nothing, so it is
// left out.
func columnChangeText(change api.ColumnChange, named api.ReferencedName) (string, bool) {
	switch {
	case change.Old != nil && change.New != nil:
		return namedValue(change.Old, named.Old) + " → " + namedValue(change.New, named.New), true
	case change.New != nil && !isJSONNull(change.New):
		return namedValue(change.New, named.New), true
	case change.Old != nil && !isJSONNull(change.Old):
		return namedValue(change.Old, named.Old), true
	default:
		return "", false
	}
}

func namedValue(raw json.RawMessage, name *string) string {
	if name == nil {
		return changeValue(raw)
	}
	return changeValue(raw) + " (" + *name + ")"
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// changeValue prints a string bare and anything else as its JSON, on one line
// and cut at changeValueWidth.
func changeValue(raw json.RawMessage) string {
	if isJSONNull(raw) {
		return orDash("")
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		text = string(raw)
	}
	first, _, cut := strings.Cut(strings.TrimRight(text, "\n"), "\n")
	if runes := []rune(first); len(runes) > changeValueWidth {
		first, cut = string(runes[:changeValueWidth]), true
	}
	if cut {
		first += "…"
	}
	return first
}
