package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

// repoFlagCommand builds a bare command carrying just --repo, so the flag
// plumbing is testable without standing up an API client.
func repoFlagCommand() *cobra.Command {
	var repo string
	cmd := &cobra.Command{Use: "test", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.Flags().StringVar(&repo, "repo", "", "")
	return cmd
}

func TestRepoFlagValue_AbsentIsNoFilter(t *testing.T) {
	withRegistry(t, `{"repos":[{"name":"dotfiles"}]}`)
	cmd := repoFlagCommand()
	if err := cmd.ParseFlags(nil); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if got, err := repoFlagValue(cmd, ""); got != nil || err != nil {
		t.Errorf("repoFlagValue = %v, %v, want nil, nil — an unset flag must not filter", got, err)
	}
}

// `--repo ""` asks for untagged work on a read and unlinks an item on a write,
// so it must survive validation rather than being read as an unknown name.
func TestRepoFlagValue_EmptyIsTheUntaggedQuestion(t *testing.T) {
	withRegistry(t, `{"repos":[{"name":"dotfiles"}]}`)
	cmd := repoFlagCommand()
	if err := cmd.ParseFlags([]string{"--repo", ""}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	got, err := repoFlagValue(cmd, "")
	if err != nil || got == nil || *got != "" {
		t.Errorf("repoFlagValue = %v, %v, want a pointer to \"\" — --repo \"\" asks for untagged work", got, err)
	}
}

func TestRepoFlagValue_RegisteredNamePassesThrough(t *testing.T) {
	withRegistry(t, `{"repos":[{"name":"dotfiles"}]}`)
	cmd := repoFlagCommand()
	if err := cmd.ParseFlags([]string{"--repo", "dotfiles"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	got, err := repoFlagValue(cmd, "dotfiles")
	if err != nil || got == nil || *got != "dotfiles" {
		t.Errorf("repoFlagValue = %v, %v, want dotfiles", got, err)
	}
}

// withRegistry points XDG_DATA_HOME at a temp tree holding a registry at the
// path DefaultPath() resolves to, so the tests exercise the real resolution
// rather than an override that only exists for them.
func withRegistry(t *testing.T, body string) {
	t.Helper()
	data := t.TempDir()
	dir := filepath.Join(data, "slate")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating registry dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "repos.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("writing registry: %v", err)
	}
	t.Setenv("XDG_DATA_HOME", data)
}

// A read filtering on a repo the registry does not know would answer with an
// empty list and exit 0, the same answer a repo with no work gets. Each read
// refuses it as a usage mistake before reaching the API.
func TestAReadRefusesAnUnregisteredRepo(t *testing.T) {
	withRegistry(t, `{"repos":[{"name":"dotfiles"}]}`)
	for _, args := range [][]string{
		{"issues", "list", "--repo", "dotfile"},
		{"next", "--repo", "dotfile"},
		{"dependencies", "tree", "--repo", "dotfile"},
	} {
		if got := runTree(t, args...); got != 2 {
			t.Errorf("%v exit = %d, want 2", args, got)
		}
	}
}
