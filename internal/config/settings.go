package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// SettingsFileName is wyrm's global, cross-project preferences file.
const SettingsFileName = "config.toml"

// DefaultSharedDir is used as the shared config directory when Settings.SharedDir
// is unset.
const DefaultSharedDir = "~/.config/wyrm/settings"

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

// resolvedSharedDir returns the absolute shared config directory, expanding
// "~" and $VARS and defaulting to DefaultSharedDir when unset.
func (s *Settings) resolvedSharedDir() (string, error) {
	dir := s.SharedDir
	if dir == "" {
		dir = DefaultSharedDir
	}
	dir = os.ExpandEnv(dir)
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
	}
	return filepath.Abs(dir)
}

// SharedConfigPath returns the path to the shared config file for the
// project rooted at dir: "<folderName>.wyrm.toml" inside the shared
// config directory.
func (s *Settings) SharedConfigPath(dir string) (string, error) {
	sharedDir, err := s.resolvedSharedDir()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(sharedDir, filepath.Base(abs)+DefaultFileName), nil
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
