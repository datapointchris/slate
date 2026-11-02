package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/datapointchris/goclilogin"
)

// Login is the seam between slate's settings and goclilogin's, so what it carries
// across is worth pinning. The keyring service is a deployed identifier that
// several released versions already wrote under, and StateDir has to stay on
// slate's own state directory rather than take goclilogin's default — two
// versions naming different lock files would not exclude each other during an
// upgrade.
func TestLoginCarriesTheDeployedIdentifiers(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/xdgstate")

	login := Login(goclilogin.Deployment{
		Issuer:   goclilogin.Setting{Value: "https://auth.example.com"},
		ClientID: goclilogin.Setting{Value: "slate-cli-somehost"},
	})
	if login.Issuer != "https://auth.example.com" {
		t.Errorf("Issuer = %q", login.Issuer)
	}
	if login.ClientID != "slate-cli-somehost" {
		t.Errorf("ClientID = %q", login.ClientID)
	}
	if login.KeyringService != "slate-cli" {
		t.Errorf("KeyringService = %q, want slate-cli", login.KeyringService)
	}
	if login.StateDir != "/tmp/xdgstate/slate" {
		t.Errorf("StateDir = %q, want slate's own state directory", login.StateDir)
	}
}

// writeMachineConfig puts a config file where MachineConfigPath resolves to.
func writeMachineConfig(t *testing.T, body string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "slate")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(body), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	t.Setenv("XDG_CONFIG_HOME", root)
}

func TestMachineConfigPathIsTheToolsOwnConfigDirectory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdgconfig")
	if got, want := MachineConfigPath(), "/tmp/xdgconfig/slate/config.yml"; got != want {
		t.Errorf("MachineConfigPath() = %q, want %q", got, want)
	}
}

func TestLoadMachineConfigReadsReposRegistry(t *testing.T) {
	writeMachineConfig(t, "repos_registry: ~/.local/share/repos.json\n")
	if got, want := LoadMachineConfig().ReposRegistry, "~/.local/share/repos.json"; got != want {
		t.Errorf("ReposRegistry = %q, want %q", got, want)
	}
}

// The config is optional, so its absence is the zero value rather than a failure.
// A machine keeping the registry where slate expects it holds no config file.
func TestLoadMachineConfigTreatsAnAbsentFileAsEmpty(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if got := LoadMachineConfig().ReposRegistry; got != "" {
		t.Errorf("ReposRegistry = %q, want empty for an absent config", got)
	}
}

// A malformed config falls through for the same reason an absent one does:
// erroring would break the machine that needs no config at all.
func TestLoadMachineConfigTreatsMalformedYAMLAsEmpty(t *testing.T) {
	writeMachineConfig(t, "repos_registry: [not, a, string\n")
	if got := LoadMachineConfig().ReposRegistry; got != "" {
		t.Errorf("ReposRegistry = %q, want empty for a malformed config", got)
	}
}

func TestExpandTildeResolvesALeadingHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	if got, want := ExpandTilde("~/.local/share/repos.json"), filepath.Join(home, ".local", "share", "repos.json"); got != want {
		t.Errorf("ExpandTilde = %q, want %q", got, want)
	}
	for _, path := range []string{"/absolute/repos.json", "relative/repos.json", ""} {
		if got := ExpandTilde(path); got != path {
			t.Errorf("ExpandTilde(%q) = %q, want it untouched", path, got)
		}
	}
}

func TestStatePathIsTheToolsOwnStateDirectory(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/xdgstate")
	if got, want := StatePath(), "/tmp/xdgstate/slate"; got != want {
		t.Errorf("StatePath() = %q, want %q", got, want)
	}
	if got, want := StatePath("slate-cli-archlinux.refresh.lock"), "/tmp/xdgstate/slate/slate-cli-archlinux.refresh.lock"; got != want {
		t.Errorf("StatePath(name) = %q, want %q", got, want)
	}
}

func TestStatePathFallsBackToLocalState(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/tester")
	if got, want := StatePath(), "/home/tester/.local/state/slate"; got != want {
		t.Errorf("StatePath() = %q, want %q", got, want)
	}
}
