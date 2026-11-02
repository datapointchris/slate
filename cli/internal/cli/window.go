package cli

import (
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/datapointchris/goclikit"
	"github.com/spf13/cobra"

	"github.com/datapointchris/slate/cli/internal/api"
)

// windowForms is the help line naming what --since and --until each take.
const windowForms = "a span back from now (30m, 12h, 7d, 2w), a day (2026-10-01) or an RFC 3339 instant"

var (
	windowSpan      = regexp.MustCompile(`^(\d+)([smhdw])$`)
	windowMonthSpan = regexp.MustCompile(`^\d+(mo|M|y|Y)$`)
)

// addWindowFlags registers --since and --until on a read bounded by time.
// rows names what the window narrows, such as "issues closed" or "work done".
func addWindowFlags(cmd *cobra.Command, rows string) {
	cmd.Flags().String("since", "", fmt.Sprintf("Only %s at or after this: %s", rows, windowForms))
	cmd.Flags().String("until", "", fmt.Sprintf("Only %s before this; a day includes all of it", rows))
}

// readWindow resolves --since and --until to the window the API takes. A day
// is read in this machine's zone.
func readWindow(cmd *cobra.Command) (api.Window, error) {
	since, _ := cmd.Flags().GetString("since")
	until, _ := cmd.Flags().GetString("until")
	window, err := resolveWindow(since, until, time.Now(), time.Local)
	if err != nil {
		return api.Window{}, goclikit.UsageError(err)
	}
	return window, nil
}

// resolveWindow reads both flag values. An empty one leaves that end open.
func resolveWindow(since, until string, now time.Time, loc *time.Location) (api.Window, error) {
	var window api.Window
	var err error
	if since != "" {
		if window.Since, err = resolveBound(since, false, now, loc); err != nil {
			return api.Window{}, fmt.Errorf("--since %q: %w", since, err)
		}
	}
	if until != "" {
		if window.Until, err = resolveBound(until, true, now, loc); err != nil {
			return api.Window{}, fmt.Errorf("--until %q: %w", until, err)
		}
	}
	return window, nil
}

// resolveBound reads one --since or --until value as an instant.
//
// A span counts back from now. Days and weeks step the calendar, so 1d at
// 09:00 is 09:00 the day before even across an offset change. A day is its
// first instant on --since, and the next day's first instant on --until, so
// the whole day is inside the window.
func resolveBound(value string, until bool, now time.Time, loc *time.Location) (time.Time, error) {
	if m := windowSpan.FindStringSubmatch(value); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return time.Time{}, err
		}
		switch m[2] {
		case "s":
			return now.Add(-time.Duration(n) * time.Second), nil
		case "m":
			return now.Add(-time.Duration(n) * time.Minute), nil
		case "h":
			return now.Add(-time.Duration(n) * time.Hour), nil
		case "d":
			return now.AddDate(0, 0, -n), nil
		default:
			return now.AddDate(0, 0, -7*n), nil
		}
	}
	if windowMonthSpan.MatchString(value) {
		return time.Time{}, fmt.Errorf("a month or a year has no fixed length; write weeks (26w) or a day")
	}
	if day, err := time.ParseInLocation(dayLayout, value, loc); err == nil {
		if until {
			return time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, loc), nil
		}
		return day, nil
	}
	if at, err := time.Parse(time.RFC3339, value); err == nil {
		return at, nil
	}
	return time.Time{}, fmt.Errorf("want %s", windowForms)
}
