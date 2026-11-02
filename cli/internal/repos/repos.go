// Package repos reads the repo registry that names what `--repo` may be set to.
//
// Validation lives in the client rather than in the API: the registry is a file
// on the machine running the CLI, and the API runs in a container that has never
// seen it.
//
// The registry is optional. Where it does not exist, `--repo` accepts anything —
// refusing to file work on a machine without a registry would make the tag
// harder to set than to leave wrong.
package repos

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/datapointchris/slate/cli/internal/config"
)

type registry struct {
	Repos []struct {
		Name string `json:"name"`
	} `json:"repos"`
}

// Registry is the set of known repo names, empty when the file is absent.
type Registry struct {
	names map[string]bool
	path  string
}

// DefaultPath is where this machine keeps the registry: $SLATE_REPOS_REGISTRY,
// then repos_registry in slate's config, then the CLI's own XDG data directory.
//
// The last rung is the compiled default and names nothing outside this tool's
// own directories, which is what keeps a generic tool generic. The two above it
// are how a machine says the file is maintained elsewhere. A machine sharing one
// registry between tools declares that in config rather than linking slate's data
// directory at it: a symlink is declared nowhere and reported by nothing, so a
// clobbered link forks the registry silently with both copies still parsing.
//
// Every variable read here is SLATE_-prefixed, and an unprefixed one is not a rung.
// A shared variable is set in a shell profile, so a process that sources none —
// a systemd timer, anything unattended — resolves the default instead and exits
// 0 having read a file nobody meant. That empties the rung in exactly the runs it
// would exist to serve. Config reaches every process and is the machine layer
// already.
func DefaultPath() string {
	declared := os.Getenv("SLATE_REPOS_REGISTRY")
	if declared == "" {
		declared = config.ReposRegistry()
	}
	if declared != "" {
		return config.ExpandTilde(declared)
	}
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "slate", "repos.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "slate", "repos.json")
}

// Load reads the registry at path. A missing file is not an error: it yields a
// registry that accepts anything.
func Load(path string) (*Registry, error) {
	if path == "" {
		return &Registry{}, nil
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Registry{path: path}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading repo registry %s: %w", path, err)
	}
	var parsed registry
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("parsing repo registry %s: %w", path, err)
	}
	names := make(map[string]bool, len(parsed.Repos))
	for _, repo := range parsed.Repos {
		names[repo.Name] = true
	}
	return &Registry{names: names, path: path}, nil
}

// Available reports whether the registry was found and had any repos in it.
func (r *Registry) Available() bool {
	return r != nil && len(r.names) > 0
}

// Names returns every registered repo name, sorted.
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	names := make([]string, 0, len(r.names))
	for name := range r.names {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Validate rejects a repo name the registry does not know. An empty name is the
// deliberate "not repo work" case and always passes; so does any name when the
// registry is unavailable.
func (r *Registry) Validate(name string) error {
	if name == "" || !r.Available() || r.names[name] {
		return nil
	}
	if suggestions := r.suggest(name); len(suggestions) > 0 {
		return fmt.Errorf("unknown repo %q — did you mean %s?", name, strings.Join(suggestions, ", "))
	}
	return fmt.Errorf("unknown repo %q — see %s for the registered names", name, r.path)
}

// Knows reports whether name is a registered repo.
//
// The inverse question to Validate, and a separate method because the two want
// opposite answers: Validate wants a repo tag to BE a registry name, and this
// exists to refuse a PROJECT name that is one. Case- and space-insensitive,
// because "Webapp " names the same thing "webapp" does and a ban a capital
// letter walks around is decoration.
func (r *Registry) Knows(name string) bool {
	if !r.Available() {
		return false
	}
	wanted := strings.ToLower(strings.TrimSpace(name))
	if wanted == "" {
		return false
	}
	for known := range r.names {
		if strings.ToLower(known) == wanted {
			return true
		}
	}
	return false
}

// suggest returns registered names sharing a prefix or containing the input,
// which covers the realistic typo: a near-miss on a name you already know.
func (r *Registry) suggest(name string) []string {
	lowered := strings.ToLower(name)
	var matches []string
	for _, known := range r.Names() {
		lowerKnown := strings.ToLower(known)
		if strings.Contains(lowerKnown, lowered) || strings.HasPrefix(lowered, lowerKnown) {
			matches = append(matches, known)
		}
	}
	if len(matches) > 3 {
		matches = matches[:3]
	}
	return matches
}
