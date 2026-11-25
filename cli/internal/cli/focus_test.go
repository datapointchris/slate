package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/datapointchris/goclikit"

	"github.com/datapointchris/slate/cli/internal/api"
)

func TestFocusSet_SendsEachShareByItsPursuitsNumber(t *testing.T) {
	var sent api.FocusSetInput
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"number":2950,"name":"Data Warehouse","status":"active"}`))
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&sent)
		_, _ = w.Write([]byte(`{"focus":null,"side_effects":[],"warnings":[]}`))
	}))
	t.Cleanup(server.Close)
	handToken(t, server.URL, "handed-access-token")

	if _, err := runService(t, "focus", "set", "--statement", "Warehouse first", "--until", "2099-12-31",
		"--share", "Data Warehouse=0.6", "--share", "#3010=0.25"); err != nil {
		t.Fatalf("run: %v", err)
	}
	want := []api.FocusShareSet{{Pursuit: 2950, Share: 0.6}, {Pursuit: 3010, Share: 0.25}}
	if sent.Statement != "Warehouse first" || sent.UntilDate != "2099-12-31" || !slices.Equal(sent.Shares, want) {
		t.Errorf("sent %+v, want the statement, the day and shares %v", sent, want)
	}
}

func TestFocusUsage_PrintsEachPursuitByDayInTheLastDaysOrder(t *testing.T) {
	for _, test := range []struct {
		name    string
		noFocus bool
		header  string
		// rows are each row's first fields, in the order printed.
		rows [][]string
	}{
		{
			name: "a focus in force", header: "Focus 3, set",
			rows: [][]string{
				{"#2985", "0.15", "(rest)", "·", "1.00", "Tidy"},
				{"#2984", "0.45", "0.08", "0.88", "▃▅█", "Lock"},
				{"#2983", "0.25", "0.07", "0.82", "▄▆█", "Finish"},
				{"#2989", "0.15", "(rest)", "0.65", "0.05", "█▆▆", "Talk"},
				{"#2992", "·", "0.20", "·", "██", "Paint"},
			},
		},
		{
			name: "no focus in force", noFocus: true, header: "No focus is in force",
			rows: [][]string{
				{"#2985", "·", "·", "·", "Tidy"},
				{"#2983", "·", "0.30", "·", "█▆", "Finish"},
				{"#2984", "·", "0.70", "·", "▇█", "Lock"},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc((&pursuitsStandIn{noFocus: test.noFocus}).serve))
			t.Cleanup(server.Close)
			handToken(t, server.URL, "handed-access-token")

			out, err := runService(t, "focus", "usage")
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if !strings.HasPrefix(out, test.header) {
				t.Errorf("the header should open %q:\n%s", test.header, out)
			}
			var rows [][]string
			for line := range strings.SplitSeq(out, "\n") {
				if fields := strings.Fields(line); len(fields) > 0 && strings.HasPrefix(fields[0], "#") {
					rows = append(rows, fields)
				}
			}
			startsAs := func(row, want []string) bool { return len(row) >= len(want) && slices.Equal(row[:len(want)], want) }
			if !slices.EqualFunc(rows, test.rows, startsAs) {
				t.Errorf("printed rows %q, want rows starting %q:\n%s", rows, test.rows, out)
			}
		})
	}
}

func TestFocusSet_RefusesAMalformedShareBeforeAnyRequest(t *testing.T) {
	for _, share := range []string{"Data Warehouse", "=0.5", "Data Warehouse=1.5", "Data Warehouse=half"} {
		t.Run(share, func(t *testing.T) {
			var sent int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { sent++ }))
			t.Cleanup(server.Close)
			handToken(t, server.URL, "handed-access-token")

			_, err := runService(t, "focus", "set", "--statement", "s", "--until", "2099-12-31", "--share", share)
			if !errors.Is(err, goclikit.ErrUsage) || sent != 0 {
				t.Errorf("--share %q answered %v after %d requests, want a usage error before any", share, err, sent)
			}
		})
	}
}
