package cli

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// dayLayout is how the API writes a calendar day.
const dayLayout = "2006-01-02"

// localDay is the day an instant falls on in this machine's zone. The API sends
// instants in UTC, so formatting one as it arrives prints the UTC day, which is
// tomorrow's date every evening for anyone west of UTC.
func localDay(t time.Time) string {
	return t.Local().Format(dayLayout)
}

// zoneinfoDirs are the paths a tzdata file lives under. The IANA name is
// whatever follows one of them, so "/usr/share/zoneinfo/US/Eastern" is
// "US/Eastern". macOS ships the same tree under /var/db/timezone.
var zoneinfoDirs = []string{
	"/usr/share/zoneinfo/",
	"/usr/lib/zoneinfo/",
	"/usr/share/lib/zoneinfo/",
	"/var/db/timezone/zoneinfo/",
}

// LocalZoneName is the IANA name of this machine's timezone, or "" when it
// cannot be read.
//
// time.Local carries the right offset rules but not the name: Go reports
// "Local" for a zone loaded from /etc/localtime, and the server needs a name it
// can look up. $TZ and that symlink are the two places the name is written.
//
// An empty return is not a failure to report. The caller leaves the parameter
// off and the server reads the day in the user's timezone preference.
func LocalZoneName() string {
	name := zoneFromEnv(os.Getenv("TZ"))
	if name == "" {
		name = zoneFromLocaltimeLink("/etc/localtime")
	}
	if !zoneExists(name) {
		return ""
	}
	return name
}

// zoneExists reports whether the tzdata database carries this name.
//
// $TZ takes a POSIX rule string as well as a name — "UTC0",
// "PST8PDT,M3.2.0/2,M11.1.0/2" — and neither names a zone file. Go reads the
// rule for its offsets, so every other command works. Sending one to the server
// is a 422 that turns one section of a report into an error exit.
//
// An empty name is not a name either. time.LoadLocation("") answers UTC without
// error, so the check has to come first.
func zoneExists(name string) bool {
	if name == "" {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}

// zoneFromEnv reads $TZ. A leading colon is allowed by POSIX and is part of the
// spelling rather than the name. A value that is an absolute path is a tzdata
// file, so it goes through the same trimming as the symlink.
func zoneFromEnv(tz string) string {
	tz = strings.TrimPrefix(tz, ":")
	if tz == "" || tz == "Local" {
		return ""
	}
	if strings.HasPrefix(tz, "/") {
		return trimZoneinfoDir(tz)
	}
	return tz
}

// zoneFromLocaltimeLink resolves /etc/localtime to the tzdata file it names.
// A container image usually ships a copy rather than a symlink, and a copy
// carries no name, so this returns "".
func zoneFromLocaltimeLink(path string) string {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	return trimZoneinfoDir(target)
}

// trimZoneinfoDir turns a tzdata path into the IANA name under it, or "" when
// the path sits outside every known zoneinfo tree.
func trimZoneinfoDir(path string) string {
	for _, dir := range zoneinfoDirs {
		if name, found := strings.CutPrefix(path, dir); found && name != "" {
			return name
		}
	}
	return ""
}
