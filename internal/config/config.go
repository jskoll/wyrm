// Package config loads and validates wyrm session configuration files.
package config

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// DefaultFileName is the config file wyrm looks for first.
const DefaultFileName = ".wyrm.toml"

// LegacyFileName is the original tmux-session config name, still supported.
const LegacyFileName = ".tmuxconfig"

// defaultConfigData is the built-in config used when neither DefaultFileName
// nor LegacyFileName is found in the current directory.
//
//go:embed default.wyrm.toml
var defaultConfigData []byte

// Config is the root of a wyrm config file.
type Config struct {
	Session Session  `toml:"session"`
	Windows []Window `toml:"windows"`
}

// Session describes the tmux session and its lifecycle hooks.
type Session struct {
	Name           string `toml:"name"`
	Root           string `toml:"root"`
	OnProjectStart string `toml:"on_project_start"`
	OnProjectExit  string `toml:"on_project_exit"`
	StartupWindow  string `toml:"startup_window"`
	StartupPane    *int   `toml:"startup_pane"` // nil = unset; 0 is a valid pane
}

// Window is one tmux window, laid out either by a split tree or a flat pane
// list (legacy format).
type Window struct {
	Name      string  `toml:"name"`
	Layout    string  `toml:"layout"`
	Splits    []Split `toml:"splits"`
	Panes     []Pane  `toml:"panes"`
	PreWindow string  `toml:"pre_window"`
}

// Split is a node in a window's split tree.
type Split struct {
	Type     string  `toml:"type"` // "", "h"/"horizontal", "v"/"vertical"
	Size     int     `toml:"size"` // percentage for the new pane; 0 = tmux default
	Command  string  `toml:"command"`
	Children []Split `toml:"children"`
}

// Pane is one entry in the legacy flat pane list.
type Pane struct {
	Command string `toml:"command"`
}

// Discover returns the config file to use when none was given: DefaultFileName
// in the current directory, falling back to LegacyFileName.
func Discover() (string, error) {
	for _, name := range []string{DefaultFileName, LegacyFileName} {
		if _, err := os.Stat(name); err == nil {
			return name, nil
		}
	}
	return "", fmt.Errorf("no %s or %s in the current directory (or pass -config)", DefaultFileName, LegacyFileName)
}

// Load reads, parses, and validates a config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &cfg, nil
}

// LoadDefault parses and validates the built-in fallback config, used when no
// config file is found in the current directory (see Discover).
func LoadDefault() (*Config, error) {
	var cfg Config
	if err := toml.Unmarshal(defaultConfigData, &cfg); err != nil {
		return nil, fmt.Errorf("parsing default config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("default config: %w", err)
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	if c.Session.Name == "" && c.Session.Root == "" {
		return errors.New("config must set session.name or session.root")
	}
	for _, w := range c.Windows {
		if err := validateSplits(w.Name, w.Splits); err != nil {
			return err
		}
	}
	return nil
}

func validateSplits(window string, splits []Split) error {
	for i, s := range splits {
		if s.Size < 0 || s.Size > 99 {
			return fmt.Errorf("window %q split %d: size must be 1-99, got %d", window, i, s.Size)
		}
		switch strings.ToLower(s.Type) {
		case "", "h", "horizontal", "v", "vertical":
		default:
			return fmt.Errorf("window %q split %d: unknown type %q (use h/horizontal or v/vertical)", window, i, s.Type)
		}
		if err := validateSplits(window, s.Children); err != nil {
			return err
		}
	}
	return nil
}

// Resolve returns the session name and absolute root directory, deriving the
// name from the root's basename when unset. Root supports $VAR expansion.
func (s Session) Resolve() (name, absRoot string, err error) {
	root := os.ExpandEnv(s.Root)
	absRoot, err = filepath.Abs(root)
	if err != nil {
		return "", "", fmt.Errorf("resolving root %q: %w", s.Root, err)
	}
	name = s.Name
	if name == "" {
		name = filepath.Base(absRoot)
	}
	return name, absRoot, nil
}
