package cli

import (
	"os"
	"os/exec"
	"strings"

	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/goselfupdate"
	"github.com/spf13/cobra"
)

// newUpdateCommand replaces this binary with the newest published release.
//
// TagPrefix is what makes it resolve the right thing. The CLI is a nested
// module released under cli/v1.2.3 so its tags never collide with the API's,
// and GitHub's "latest release" endpoint is repository-wide — without the
// prefix it would return whatever the repository released most recently, which
// may be the API rather than this program.
//
// A locally built binary refuses to update: git describe returns the prefixed
// tag, which is not a semantic version, so it reports as a development build.
// Only CI strips the prefix, so only a released binary can replace itself.
func updateConfig() goselfupdate.Config {
	return goselfupdate.Config{
		Owner:     "datapointchris",
		Repo:      "slate",
		Binary:    "slate",
		Version:   version,
		TagPrefix: "cli/",
		// TokenFunc, not Token: Execute builds this config on every invocation
		// to run the daily update check's gate, and that gate is designed to
		// cost nothing. Calling githubToken() here would put a `gh auth token`
		// subprocess in front of every `slate` command, including the ~364 out of
		// 365 that decline to check at all.
		TokenFunc: githubToken,
	}
}

func newUpdateCommand() *cobra.Command {
	return goclikit.UpdateCommand(updateConfig())
}

// githubToken resolves a GitHub credential: the environment first, then the gh
// CLI's stored token.
//
// goselfupdate reads $GITHUB_TOKEN and $GH_TOKEN on its own, so this only adds
// the third source. A token lifts GitHub's 60-requests-per-hour limit on
// unauthenticated reads, which a shared address reaches quickly.
func githubToken() string {
	for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if token := os.Getenv(name); token != "" {
			return token
		}
	}
	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
