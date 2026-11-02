package repos

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRegistry(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "repos.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing registry: %v", err)
	}
	return path
}

const sample = `{"repos":[{"name":"dotfiles"},{"name":"webapp"},{"name":"notes"},{"name":"notes-content"}]}`

func TestKnownNamePasses(t *testing.T) {
	registry, err := Load(writeRegistry(t, sample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := registry.Validate("webapp"); err != nil {
		t.Errorf("registered name must validate, got %v", err)
	}
}

func TestEmptyNameIsNotRepoWork(t *testing.T) {
	registry, _ := Load(writeRegistry(t, sample))
	if err := registry.Validate(""); err != nil {
		t.Errorf("an empty repo is the deliberate not-repo-work case, got %v", err)
	}
}

func TestUnknownNameSuggestsNearMatches(t *testing.T) {
	registry, _ := Load(writeRegistry(t, sample))
	err := registry.Validate("notes-conten")
	if err == nil {
		t.Fatal("an unregistered repo must be rejected")
	}
	if !strings.Contains(err.Error(), "notes-content") {
		t.Errorf("expected a suggestion naming notes-content, got %q", err)
	}
}

func TestUnknownNameWithNoNearMatchPointsAtTheRegistry(t *testing.T) {
	registry, _ := Load(writeRegistry(t, sample))
	err := registry.Validate("zzzz")
	if err == nil {
		t.Fatal("an unregistered repo must be rejected")
	}
	if !strings.Contains(err.Error(), "repos.json") {
		t.Errorf("expected the message to name the registry, got %q", err)
	}
}

// A machine without a registry must still be able to file work — the registry is
// advisory, so an absent one accepts anything rather than blocking the command.
func TestMissingRegistryAcceptsAnything(t *testing.T) {
	registry, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("a missing registry must not be an error, got %v", err)
	}
	if registry.Available() {
		t.Error("a missing registry must report itself unavailable")
	}
	if err := registry.Validate("whatever"); err != nil {
		t.Errorf("validation must be skipped without a registry, got %v", err)
	}
}

func TestMalformedRegistryIsAnError(t *testing.T) {
	if _, err := Load(writeRegistry(t, "{not json")); err == nil {
		t.Error("a corrupt registry must surface rather than silently disabling validation")
	}
}

// withoutMachineConfig points XDG_CONFIG_HOME at an empty tree, so a resolution
// test sees no config file whatever this machine actually declares. Without it
// the test reads the developer's real config and fails for a reason unrelated to
// the code.
func withoutMachineConfig(t *testing.T) {
	t.Helper()
	t.Setenv("SLATE_REPOS_REGISTRY", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

// withMachineConfig writes slate's config file with the given body and points
// XDG_CONFIG_HOME at the tree holding it.
func withMachineConfig(t *testing.T, body string) {
	t.Helper()
	t.Setenv("SLATE_REPOS_REGISTRY", "")
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

// The compiled default names nothing outside the tool's own XDG data directory,
// which is what keeps a generic tool generic.
func TestDefaultPathIsTheToolsOwnDataDirectory(t *testing.T) {
	withoutMachineConfig(t)
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg")
	if got, want := DefaultPath(), "/tmp/xdg/slate/repos.json"; got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

// $REPOS_JSON was the rung between the config and the default, shared unprefixed
// by every tool that read the registry. It is gone, and this is the assertion
// that it stays gone: a variable set in ~/.env is invisible to a process that
// sources no profile, so the rung was empty in exactly the unattended runs it
// existed to serve. slate reads no variable that is not SLATE_-prefixed.
func TestTheUnprefixedSharedVariableIsNeverConsulted(t *testing.T) {
	withoutMachineConfig(t)
	t.Setenv("REPOS_JSON", "/shared/repos.json")
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg")
	if got, want := DefaultPath(), "/tmp/xdg/slate/repos.json"; got != want {
		t.Errorf("DefaultPath() = %q, want %q — $REPOS_JSON must not be read", got, want)
	}
}

// A machine that maintains its registry elsewhere says so in its config, which
// is the layer that reaches an unattended process. This replaced a hand-made
// symlink from the tool's data directory, which was declared nowhere.
func TestTheConfigKeyBeatsTheToolsOwnDirectory(t *testing.T) {
	withMachineConfig(t, "repos_registry: /declared/repos.json\n")
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg")
	if got, want := DefaultPath(), "/declared/repos.json"; got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

// The variable is this shell and the config is this machine, so the variable
// wins — which is what makes a one-off run against another registry possible
// without editing the machine's config.
func TestThePrefixedVariableBeatsTheConfigKey(t *testing.T) {
	withMachineConfig(t, "repos_registry: /declared/repos.json\n")
	t.Setenv("SLATE_REPOS_REGISTRY", "/from/env/repos.json")
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg")
	if got, want := DefaultPath(), "/from/env/repos.json"; got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

// A config file is hand-edited, so it carries ~ rather than an absolute path.
// The variable gets the same treatment: it is typed by hand too.
func TestALeadingTildeExpandsInBothDeclaredLayers(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	want := filepath.Join(home, ".local", "share", "repos.json")

	withMachineConfig(t, "repos_registry: ~/.local/share/repos.json\n")
	if got := DefaultPath(); got != want {
		t.Errorf("config key: DefaultPath() = %q, want %q", got, want)
	}

	t.Setenv("SLATE_REPOS_REGISTRY", "~/.local/share/repos.json")
	if got := DefaultPath(); got != want {
		t.Errorf("env var: DefaultPath() = %q, want %q", got, want)
	}
}

// An unreadable or malformed config is not an error: the file is optional, and
// failing here would break the machine that keeps its registry exactly where slate
// expects it — the case that needs no config at all.
func TestAMalformedConfigFallsThroughToTheDefault(t *testing.T) {
	withMachineConfig(t, "repos_registry: [not, a, string\n")
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg")
	if got, want := DefaultPath(), "/tmp/xdg/slate/repos.json"; got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestUnknownNameNamesTheRegistryItRead(t *testing.T) {
	path := writeRegistry(t, sample)
	registry, _ := Load(path)
	err := registry.Validate("zzzz")
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("the message must name the registry actually read, got %v", err)
	}
}

func TestKnowsIdentifiesARepoName(t *testing.T) {
	path := writeRegistry(t, `{"repos":[{"name":"webapp"},{"name":"dotfiles"}]}`)
	registry, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	for _, name := range []string{"webapp", "Webapp", "  dotfiles  ", "DOTFILES"} {
		if !registry.Knows(name) {
			t.Errorf("Knows(%q) = false; a ban a capital letter walks around is decoration", name)
		}
	}
	for _, name := range []string{"", "webapp sync improvements", "Extract xx from dotfiles"} {
		if registry.Knows(name) {
			t.Errorf("Knows(%q) = true; bounded work named after a repo is still bounded work", name)
		}
	}
}

func TestKnowsIsFalseWithoutARegistry(t *testing.T) {
	// Same policy as Validate: no registry means no opinion, because refusing
	// to file work on a machine without one is worse than the wrong name.
	registry, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if registry.Knows("webapp") {
		t.Error("a missing registry must not ban anything")
	}
}
