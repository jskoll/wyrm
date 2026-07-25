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

	// dir is the directory the config was loaded from. Relative roots resolve
	// against it rather than against the process's working directory, so a
	// config means the same thing however wyrm was invoked — see Session.Resolve
	// and Config.Dir. Zero for a Config built in memory (tests, the built-in
	// default), which falls back to the working directory.
	dir string

	// warnings are non-fatal problems found while validating: things that will
	// build, but not the way the config's author probably meant. Callers print
	// them; they never stop a session being created.
	warnings []string
}

// Dir returns the directory this config was loaded from, or "" for a config
// that didn't come from a file.
func (c *Config) Dir() string { return c.dir }

// Warnings returns non-fatal problems found while validating the config.
func (c *Config) Warnings() []string { return c.warnings }

// Session describes the tmux session and its lifecycle hooks.
type Session struct {
	Name           string `toml:"name,omitempty"`
	Root           string `toml:"root,omitempty"`
	OnProjectStart string `toml:"on_project_start,omitempty"`
	OnProjectExit  string `toml:"on_project_exit,omitempty"`
	StartupWindow  string `toml:"startup_window,omitempty"`
	StartupPane    *int   `toml:"startup_pane,omitempty"` // nil = unset; 0 is a valid pane
}

// Window is one tmux window, laid out either by a split tree or a flat pane
// list (legacy format).
type Window struct {
	Name      string  `toml:"name,omitempty"`
	Layout    string  `toml:"layout,omitempty"`
	Splits    []Split `toml:"splits,omitempty"`
	Panes     []Pane  `toml:"panes,omitempty"`
	PreWindow string  `toml:"pre_window,omitempty"`
}

// Split is a node in a window's split tree.
type Split struct {
	Type     string  `toml:"type,omitempty"` // "", "h"/"horizontal", "v"/"vertical"
	Size     int     `toml:"size,omitempty"` // percentage for the new pane; 0 = tmux default
	Command  string  `toml:"command,omitempty"`
	Children []Split `toml:"children,omitempty"`
}

// Pane is one entry in the legacy flat pane list.
type Pane struct {
	Command string `toml:"command,omitempty"`
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
	if abs, err := filepath.Abs(path); err == nil {
		cfg.dir = filepath.Dir(abs)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if filepath.Base(path) == LegacyFileName {
		cfg.warnings = append(cfg.warnings, fmt.Sprintf(
			"%s is deprecated and will be removed in 1.0; rename it to %s", LegacyFileName, DefaultFileName))
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
	// session.Create refuses a config with no windows, so rejecting it here
	// keeps `wyrm validate` from blessing a config `wyrm` will not build.
	if len(c.Windows) == 0 {
		return errors.New("config defines no windows (add at least one [[windows]])")
	}
	for _, w := range c.Windows {
		if err := validateSplits(w.Name, w.Splits); err != nil {
			return err
		}
		c.warnings = append(c.warnings, windowWarnings(w)...)
	}
	return nil
}

// windowWarnings reports config that parses and builds but almost certainly
// doesn't do what its author meant.
func windowWarnings(w Window) []string {
	var out []string
	name := w.Name
	if name == "" {
		name = "(unnamed)"
	}
	if len(w.Splits) > 0 && len(w.Panes) > 0 {
		out = append(out, fmt.Sprintf("window %q sets both splits and panes; panes is ignored", name))
	}
	if len(w.Splits) > 0 && w.Layout != "" {
		// Applying a named tmux layout would discard the split tree's sizes,
		// so wyrm doesn't — but silently ignoring the key looks like a bug.
		out = append(out, fmt.Sprintf(
			"window %q sets layout=%q alongside splits; layout only applies to the legacy panes list and is ignored here", name, w.Layout))
	}
	if len(w.Panes) > 0 {
		out = append(out, fmt.Sprintf(
			"window %q uses the flat panes list, which is deprecated and will be removed in 1.0; use splits", name))
	}
	if len(w.Splits) > 0 && w.Splits[0].Type != "" {
		out = append(out, fmt.Sprintf(
			"window %q: the first split sets type=%q, so it splits the window's initial pane and leaves it empty — drop the type to put this entry in the initial pane", name, w.Splits[0].Type))
	}
	return out
}

func validateSplits(window string, splits []Split) error {
	for i, s := range splits {
		// 0 means "let tmux decide", so it's accepted alongside 1-99.
		if s.Size < 0 || s.Size > 99 {
			return fmt.Errorf("window %q split %d: size must be 1-99 (or omitted for tmux's default), got %d", window, i, s.Size)
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

// ResolveEffective returns the config that would be used for this project,
// mirroring wyrm's normal discovery order: an explicit path, the discovered
// local or shared file, the user's default override, then the built-in
// default. It returns the resolved source alongside the config — a file
// path, or "built-in default" when none exists on disk. Unlike main's
// bare-`wyrm` flow, it never falls back to the interactive session picker.
func ResolveEffective(settings *Settings, explicitPath string) (*Config, string, error) {
	if explicitPath != "" {
		cfg, err := Load(explicitPath)
		if err != nil {
			return nil, "", err
		}
		return cfg, explicitPath, nil
	}

	if discovered, derr := DiscoverGlobal(settings); derr == nil {
		cfg, err := Load(discovered)
		if err != nil {
			return nil, "", err
		}
		return cfg, discovered, nil
	}

	cfg, err := LoadUserDefault()
	if err != nil {
		return nil, "", err
	}
	if cfg != nil {
		path, err := UserDefaultPath()
		if err != nil {
			return nil, "", err
		}
		return cfg, path, nil
	}

	cfg, err = LoadDefault()
	if err != nil {
		return nil, "", err
	}
	return cfg, "built-in default", nil
}

// Resolve returns the session name and absolute root directory, deriving the
// name from the root's basename when unset. Root supports "~" and $VAR
// expansion.
//
// A relative root resolves against baseDir — the directory the config was
// loaded from (see Config.Dir) — not against the process's working directory.
// That's what makes a config mean the same thing wherever wyrm runs from: the
// TUI's Projects panel and `wyrm <name>` both build sessions for configs that
// aren't in the current folder, and resolving "." against the cwd there rooted
// the session wherever the user happened to be standing. An empty baseDir
// falls back to the working directory, for a Config built in memory.
func (s Session) Resolve(baseDir string) (name, absRoot string, err error) {
	root, err := ExpandPath(s.Root)
	if err != nil {
		return "", "", fmt.Errorf("resolving session.root: %w", err)
	}
	if root == "" {
		root = "."
	}
	if !filepath.IsAbs(root) && baseDir != "" {
		root = filepath.Join(baseDir, root)
	}
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

// ExpandPath expands a leading "~" and any $VAR references in a configured
// path. An unset variable is an error rather than an empty string:
// os.ExpandEnv would quietly turn "$PROJECTS/api" into "/api" and root a
// session at a directory nobody asked for.
func ExpandPath(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	var missing []string
	expanded := os.Expand(p, func(key string) string {
		if v, ok := os.LookupEnv(key); ok {
			return v
		}
		missing = append(missing, "$"+key)
		return ""
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("undefined variable %s in %q", strings.Join(missing, ", "), p)
	}
	return ExpandTilde(expanded)
}

// ExpandTilde replaces a leading "~" or "~/" with the user's home directory.
// Shared by session.root and settings.shared_dir so wyrm's two path settings
// can't disagree about what "~" means — they did, and root silently produced a
// literal directory named "~".
func ExpandTilde(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~")), nil
}
