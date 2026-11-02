package cli

import (
	"testing"
	"time"

	"github.com/datapointchris/slate/cli/internal/api"
)

// The clock sits at 09:00 on the day New York springs forward, which is 23
// hours long. A day back, or a day's end, reached by adding hours lands an
// hour off, so each row asserts the instant rather than the date.
func TestResolveWindow(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 8, 9, 0, 0, 0, ny)
	at := func(month time.Month, day, hour, minute int) time.Time {
		return time.Date(2026, month, day, hour, minute, 0, 0, ny)
	}
	for _, tc := range []struct {
		name         string
		since, until string
		want         api.Window
		refused      bool
	}{
		{name: "seconds", since: "30s", want: api.Window{Since: now.Add(-30 * time.Second)}},
		{name: "m is minutes", since: "30m", want: api.Window{Since: at(time.March, 8, 8, 30)}},
		{name: "hours", since: "12h", want: api.Window{Since: now.Add(-12 * time.Hour)}},
		{name: "a day back keeps the clock across the change", since: "1d", want: api.Window{Since: at(time.March, 7, 9, 0)}},
		{name: "weeks", since: "2w", want: api.Window{Since: at(time.February, 22, 9, 0)}},
		{name: "a month is refused", since: "6mo", refused: true},
		{name: "a year is refused", until: "1y", refused: true},
		{name: "a day on --since is its first instant", since: "2026-03-08", want: api.Window{Since: at(time.March, 8, 0, 0)}},
		{name: "a day on --until takes in all of it", until: "2026-03-08", want: api.Window{Until: at(time.March, 9, 0, 0)}},
		{
			name: "an instant is taken as written", since: "2026-10-01T14:00:00-04:00",
			want: api.Window{Since: time.Date(2026, 10, 1, 18, 0, 0, 0, time.UTC)},
		},
		{name: "both ends together", since: "1w", until: "1d", want: api.Window{Since: at(time.March, 1, 9, 0), Until: at(time.March, 7, 9, 0)}},
		{name: "a malformed day", until: "10/01", refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveWindow(tc.since, tc.until, now, ny)
			if tc.refused {
				if err == nil {
					t.Fatalf("resolveWindow(%q, %q) = %+v, want a refusal", tc.since, tc.until, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveWindow(%q, %q): %v", tc.since, tc.until, err)
			}
			if !got.Since.Equal(tc.want.Since) || !got.Until.Equal(tc.want.Until) {
				t.Errorf("resolveWindow(%q, %q) = %v .. %v, want %v .. %v", tc.since, tc.until, got.Since, got.Until, tc.want.Since, tc.want.Until)
			}
		})
	}
}
