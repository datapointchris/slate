package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A session starting blind on another box reads from the list where each
// holder works, its worklog, and whether it is parked, so live work and
// parked work read differently.
func TestClaimsList_PrintsWhereEachHolderWorksAndWhatItWaitsOn(t *testing.T) {
	claim := func(number int, holder, session, waiting string) string {
		return fmt.Sprintf(`{"id":%d,"record_number":%d,"holder":%q,"session":%q,"host":"desk","directory":"/work/%d",`+
			`"started_ts":"2026-10-19T09:00:00Z","expires_ts":"2099-10-20T09:00:00Z","state":"live","worklog":"%d.1",`+
			`"waiting":%s,"record":{"number":%d,"kind":"issue","title":"Issue %d","status":"open"}}`,
			number, number, holder, session, number, number, waiting, number, number)
	}
	answer := "[" + claim(412, serviceClientID, "pod-7", `{"waiting_on":"slate#3 to merge","started_ts":"2026-10-19T10:00:00Z"}`) +
		"," + claim(398, "claude-code/a1b2", "a1b2", "null") + "]"
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(api.Close)
	asService(t, serviceIDP(t).URL, api.URL, serviceSecret)

	out, err := runService(t, "claims", "list")
	if err != nil {
		t.Fatal(err)
	}
	parked, live, _ := strings.Cut(out, "\n\n")
	for _, want := range []string{"#412 issue  Issue 412", "in desk:/work/412, session pod-7, worklog 412.1", "waiting on slate#3 to merge since "} {
		if !strings.Contains(parked, want) {
			t.Errorf("the waiting claim printed %q, want %q in it", parked, want)
		}
	}
	if !strings.Contains(live, "in desk:/work/398, worklog 398.1") || strings.Contains(live, "waiting") || strings.Contains(live, "session a1b2") {
		t.Errorf("the working claim printed %q, want where it works, its session left in its holder, and no wait", live)
	}
}

// A hold that goes through despite a claimed neighbor says so on stderr, under
// --json too, and names what else it did under the line naming the hold.
func TestClaimsHold_PrintsEachWarningToStderrAndEachSideEffectUnderTheHold(t *testing.T) {
	const (
		expiredEnded      = "Ended worker/old's claim on #412 as expired at 2026-10-19 08:00 UTC, before this one started."
		claimedInitiative = "#412 is in initiative #300 'Ship it', which is claimed by claude-code/lead."
	)
	answer := `{"claim":{"id":7,"record_number":412,"holder":"` + serviceClientID + `",` +
		`"started_ts":"2026-10-19T09:00:00Z","expires_ts":"2026-10-20T09:00:00Z","state":"live",` +
		`"record":{"number":412,"kind":"issue","title":"Retry the upload","status":"open"}},` +
		`"side_effects":[{"code":"expired_claim_ended","message":"` + expiredEnded + `","record":412}],` +
		`"warnings":[{"code":"claimed_initiative","message":"` + claimedInitiative + `","record":300}]}`
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(api.Close)
	asService(t, serviceIDP(t).URL, api.URL, serviceSecret)

	for _, args := range [][]string{{"claims", "hold", "412"}, {"claims", "hold", "412", "--json"}} {
		root := NewRootCommand()
		var out, errs bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&errs)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if errs.String() != "Warning: "+claimedInitiative+"\n" {
			t.Errorf("%v stderr = %q, want the warning alone", args, errs.String())
		}
		if strings.HasSuffix(args[len(args)-1], "json") {
			var envelope map[string]json.RawMessage
			if err := json.Unmarshal(out.Bytes(), &envelope); err != nil || envelope["warnings"] == nil || envelope["side_effects"] == nil {
				t.Errorf("--json printed %s (%v), want the whole answer", out.String(), err)
			}
			continue
		}
		lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
		if len(lines) != 2 || !strings.HasPrefix(lines[0], "Holding #412 Retry the upload as "+serviceClientID+" until ") ||
			lines[1] != "  "+expiredEnded {
			t.Errorf("stdout = %q, want the hold, then the side effect indented under it", out.String())
		}
	}
}
