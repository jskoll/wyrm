// Package state persists small facts about wyrm that must survive past a
// single invocation and even past a `wyrm kill` — currently just which
// project directories have ever started a session, so a lifecycle hook can
// tell a project's genuine first start from a later restart.
//
// It has no dependency on internal/config or internal/session, so it can be
// reasoned about and tested entirely on its own; internal/session consumes
// it through a small interface (HookHistory) rather than importing this
// package directly.
package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/pelletier/go-toml/v2"
)

// FileName is wyrm's small persistent-state file, stored alongside the
// global settings file.
const FileName = "state.toml"

// Store tracks which project directories have started a session before.
// The zero value is not usable — construct one with Load.
type Store struct {
	path    string
	started map[string]bool
}

type fileFormat struct {
	// Started is every project directory (absolute, as Config.Dir returns)
	// that has started a session at least once.
	Started []string `toml:"started"`
}

// Load reads the state file, returning an empty, usable Store — not an
// error — when it doesn't exist yet, the same tolerance LoadSettings gives a
// missing global settings file.
func Load() (*Store, error) {
	path, err := statePath()
	if err != nil {
		return nil, err
	}
	s := &Store{path: path, started: map[string]bool{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var ff fileFormat
	if err := toml.Unmarshal(data, &ff); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	for _, d := range ff.Started {
		s.started[d] = true
	}
	return s, nil
}

// Started reports whether dir has started a session before. Nil-safe and
// false for an empty dir, so a Config with no on-disk identity (the
// built-in default, or one built in memory) never matches.
func (s *Store) Started(dir string) bool {
	if s == nil || dir == "" {
		return false
	}
	return s.started[dir]
}

// MarkStarted records dir as started and persists immediately — there is no
// separate Save, because a start recorded but not yet on disk is exactly
// the state a crash between the two would leave wrong.
func (s *Store) MarkStarted(dir string) error {
	if s == nil || dir == "" {
		return nil
	}
	if s.started[dir] {
		return nil
	}
	s.started[dir] = true
	return s.save()
}

func (s *Store) save() error {
	dirs := make([]string, 0, len(s.started))
	for d := range s.started {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	data, err := toml.Marshal(fileFormat{Started: dirs})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}

// statePath returns the path to the state file, honoring $XDG_CONFIG_HOME
// and falling back to ~/.config — mirroring config.SettingsPath, duplicated
// rather than imported so this package stays a leaf with no dependency on
// internal/config.
func statePath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "wyrm", FileName), nil
}

func configDir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return xdg, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config"), nil
}
