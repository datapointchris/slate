package config

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// MachineConfigPath is slate's own config: $XDG_CONFIG_HOME/slate/config.yml.
func MachineConfigPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "slate", "config.yml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "slate", "config.yml")
}

// MachineConfig is what this machine says about its own layout, as opposed to
// config.toml beside it, which says which deployment to talk to.
//
// YAML rather than JSON because every key in here wants the reason it is set
// written next to it, and JSON has nowhere to put one.
type MachineConfig struct {
	// ReposRegistry names a registry maintained outside slate's own data
	// directory, which is how a machine that shares one registry between tools
	// declares it. Empty, or no config file at all, leaves the data directory as
	// the answer — so a machine keeping the registry where slate expects it needs
	// no config.
	//
	// A fact about the machine's layout rather than about the portfolio, which is
	// why it lives here and not in the registry it names. Being told a path is
	// what keeps this tool generic; compiling one in is what would not.
	ReposRegistry string `yaml:"repos_registry,omitempty"`
}

// LoadMachineConfig reads the config, answering the zero value for every way
// that can fail.
//
// The file is optional by design, so an unreadable or malformed one falls
// through to the compiled defaults rather than stopping the command. Erroring
// here would break exactly the machine that needs no config at all.
func LoadMachineConfig() MachineConfig {
	path := MachineConfigPath()
	if path == "" {
		return MachineConfig{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return MachineConfig{}
	}
	var cfg MachineConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return MachineConfig{}
	}
	return cfg
}

// ReposRegistry is the registry this machine declares, or "".
//
// Returned unexpanded so the caller applies ExpandTilde once across every rung,
// which keeps a single readable resolution order in the package that owns it.
func ReposRegistry() string {
	return LoadMachineConfig().ReposRegistry
}

// ExpandTilde resolves a leading ~, which a hand-edited config carries.
func ExpandTilde(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
}

// StatePath resolves a path under slate's own XDG state directory —
// $XDG_STATE_HOME/slate, falling back to ~/.local/state/slate. State is what the
// tool writes and the user does not: coordination files, run history, cached
// timestamps. Returns "" when the home directory cannot be resolved, which
// callers treat as "no state directory available" rather than as an error.
func StatePath(parts ...string) string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(append([]string{base, "slate"}, parts...)...)
}
