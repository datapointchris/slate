package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
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
