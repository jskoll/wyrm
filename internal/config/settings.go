package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// SettingsFileName is wyrm's global, cross-project preferences file.
const SettingsFileName = "config.toml"

// UserDefaultFileName is a user-supplied replacement for the built-in
// default config, stored alongside SettingsFileName.
const UserDefaultFileName = "default.wyrm.toml"

// ThemeFileName is the TUI's optional color override, stored alongside
// SettingsFileName.
const ThemeFileName = "theme.toml"

// DefaultSharedDir is the shared config directory used when
// Settings.SharedDir is unset, for documentation and error messages. The
// resolved path comes from defaultSharedDir, which honors $XDG_CONFIG_HOME.
const DefaultSharedDir = "~/.config/wyrm/settings"

// defaultSharedDir returns the shared config directory to use when none is
// configured: alongside the settings file, so it follows $XDG_CONFIG_HOME the
// same way SettingsPath does. Hardcoding "~/.config" meant a user with
// XDG_CONFIG_HOME set had their settings read from one place and their shared
// configs looked for in another.
func defaultSharedDir() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "wyrm", "settings"), nil
}

// Storage selects where wyrm looks for a project's config file.
type Storage string

const (
	// StorageLocal (the default) looks for DefaultFileName/LegacyFileName in
	// the current directory, as wyrm always has.
	StorageLocal Storage = "local"
	// StorageShared looks for "<folderName>.wyrm.toml" in the shared config
	// directory first, falling back to StorageLocal behavior if it's absent.
	StorageShared Storage = "shared"
)

// Settings is wyrm's global preferences, shared across all projects.
type Settings struct {
	Storage   Storage `toml:"storage"`
	SharedDir string  `toml:"shared_dir"`
	TUI       TUI     `toml:"tui"`
}

// TUI holds the interactive session manager's preferences.
//
// The bool fields are pointers so "absent" and "explicitly false" stay
// distinguishable: both of these default to on, and a plain bool would make an
// unwritten settings file indistinguishable from one that turned them off.
type TUI struct {
	// Mouse enables mouse reporting in the TUI. Defaults to true; a user who
	// would rather keep their terminal's own click-drag text selection can set
	// it to false here, or toggle it for one run with "m".
	Mouse *bool `toml:"mouse"`
	Agent Agent `toml:"agent"`
}

// Agent configures the "this pane is waiting for you" markers.
type Agent struct {
	// Enabled turns agent detection on. Defaults to true. Turning it off also
	// stops the pane captures it costs.
	Enabled *bool `toml:"enabled"`
	// Commands are the #{pane_current_command} values treated as an agent pane.
	// Empty means the built-in default (claude).
	//
	// It only widens which panes are inspected; the patterns that classify them
	// stay the built-in ones. Use Profiles to describe a different agent.
	Commands []string `toml:"commands"`
	// Profiles describe agents wyrm doesn't ship knowing about: which command
	// each runs as, and the on-screen chrome that marks it busy, blocked, or
	// idle. A non-empty list replaces the built-in profile entirely rather than
	// adding to it — otherwise one agent's chrome could decide another's state.
	Profiles []AgentProfile `toml:"profiles"`
}

// AgentProfile mirrors agent.Profile in the settings file. It is duplicated
// rather than imported so internal/config keeps no dependency on the detector,
// which is what lets internal/agent stay a leaf package.
type AgentProfile struct {
	Commands    []string `toml:"commands"`
	Busy        []string `toml:"busy"`
	Blocked     []string `toml:"blocked"`
	Idle        []string `toml:"idle"`
	BusyPattern string   `toml:"busy_pattern"`
}

// MouseEnabled reports whether the TUI should start with the mouse captured.
// Nil-safe: a nil Settings takes the defaults, which is how the TUI is
// constructed in tests and when no settings file exists.
func (s *Settings) MouseEnabled() bool {
	if s == nil || s.TUI.Mouse == nil {
		return true
	}
	return *s.TUI.Mouse
}

// AgentEnabled reports whether the TUI should look for waiting agent panes.
func (s *Settings) AgentEnabled() bool {
	if s == nil || s.TUI.Agent.Enabled == nil {
		return true
	}
	return *s.TUI.Agent.Enabled
}

// AgentCommands returns the pane commands to treat as agents; nil means the
// package default.
func (s *Settings) AgentCommands() []string {
	if s == nil {
		return nil
	}
	return s.TUI.Agent.Commands
}

// AgentProfiles returns the configured agent profiles; nil means the built-in
// one. A bare `commands` list is surfaced here as a profile carrying the
// built-in patterns, so the two settings compose the way a reader would expect:
// commands widens what the shipped detector looks at, profiles replaces it.
func (s *Settings) AgentProfiles() []AgentProfile {
	if s == nil {
		return nil
	}
	return s.TUI.Agent.Profiles
}

// SettingsPath returns the path to the global settings file, honoring
// $XDG_CONFIG_HOME and falling back to ~/.config.
func SettingsPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "wyrm", SettingsFileName), nil
}

// ThemePath returns the path to the TUI's optional color override file,
// honoring $XDG_CONFIG_HOME and falling back to ~/.config. The file is read by
// internal/tui; config only owns where it lives, alongside the other
// user-level files.
func ThemePath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "wyrm", ThemeFileName), nil
}

// UserDefaultPath returns the path to the user's default config override,
// honoring $XDG_CONFIG_HOME and falling back to ~/.config.
func UserDefaultPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "wyrm", UserDefaultFileName), nil
}

// LoadUserDefault reads, parses, and validates the user's default config
// override (see UserDefaultPath). It returns a nil Config, with no error,
// when no override file exists — callers should then fall back to
// LoadDefault.
func LoadUserDefault() (*Config, error) {
	path, err := UserDefaultPath()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return Load(path)
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

// LoadSettings reads the global settings file, returning defaults
// (StorageLocal) when it doesn't exist.
func LoadSettings() (*Settings, error) {
	path, err := SettingsPath()
	if err != nil {
		return nil, err
	}
	s := &Settings{Storage: StorageLocal}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := toml.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if s.Storage == "" {
		s.Storage = StorageLocal
	}
	if s.Storage != StorageLocal && s.Storage != StorageShared {
		return nil, fmt.Errorf("%s: storage must be %q or %q, got %q", path, StorageLocal, StorageShared, s.Storage)
	}
	return s, nil
}

// ResolvedSharedDir returns the absolute shared config directory, expanding
// "~" and $VARS and defaulting to DefaultSharedDir when unset.
func (s *Settings) ResolvedSharedDir() (string, error) {
	if s.SharedDir == "" {
		dir, err := defaultSharedDir()
		if err != nil {
			return "", err
		}
		return filepath.Abs(dir)
	}
	dir, err := ExpandPath(s.SharedDir)
	if err != nil {
		return "", fmt.Errorf("resolving shared_dir: %w", err)
	}
	return filepath.Abs(dir)
}

// SharedConfigPath returns the path to the shared config file for the
// project rooted at dir: "<folderName>.wyrm.toml" inside the shared
// config directory.
func (s *Settings) SharedConfigPath(dir string) (string, error) {
	sharedDir, err := s.ResolvedSharedDir()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(sharedDir, filepath.Base(abs)+DefaultFileName), nil
}

// EditTarget returns the path wyrm edit should open: the discovered config
// if one exists, otherwise the path a new one should be created at per
// settings.Storage — the shared path (mirroring -migrate-config's
// destination) in shared mode, DefaultFileName in the cwd otherwise.
func EditTarget(settings *Settings) (path string, exists bool, err error) {
	if discovered, derr := DiscoverGlobal(settings); derr == nil {
		return discovered, true, nil
	}
	if settings != nil && settings.Storage == StorageShared {
		cwd, err := os.Getwd()
		if err != nil {
			return "", false, err
		}
		path, err := settings.SharedConfigPath(cwd)
		if err != nil {
			return "", false, err
		}
		return path, false, nil
	}
	return DefaultFileName, false, nil
}

// DiscoverGlobal is like Discover, but honors settings.Storage: in
// StorageShared mode it looks for the shared "<folderName>.wyrm.toml" first,
// falling back to Discover's normal current-directory search if that file
// doesn't exist.
func DiscoverGlobal(settings *Settings) (string, error) {
	if settings != nil && settings.Storage == StorageShared {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		shared, err := settings.SharedConfigPath(cwd)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(shared); err == nil {
			return shared, nil
		}
	}
	return Discover()
}
