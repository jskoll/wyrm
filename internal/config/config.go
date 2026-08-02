// Package config loads and validates wyrm session configuration files.
package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
	// OnProjectFirstStart and OnProjectRestart run alongside OnProjectStart —
	// which always fires on a fresh build — distinguishing a project's
	// genuine first-ever start from a later one. Which fires is decided by
	// session.HookHistory, threaded in from outside this package: Create has
	// no way to tell the two apart on its own without it.
	OnProjectFirstStart string            `toml:"on_project_first_start,omitempty"`
	OnProjectRestart    string            `toml:"on_project_restart,omitempty"`
	StartupWindow       string            `toml:"startup_window,omitempty"`
	StartupPane         *int              `toml:"startup_pane,omitempty"` // nil = unset; 0 is a valid pane
	Env                 map[string]string `toml:"env,omitempty"`
	// Aliases are additional exact-match names FindProject resolves this
	// project by, alongside its normal session name — a short, fixed name
	// that doesn't shift as other projects come and go. Matched only for
	// exact equality, the same as the session name itself; no fuzzy
	// matching is involved.
	Aliases []string `toml:"aliases,omitempty"`

	// EnablePaneTitles turns on tmux's live pane-border status line
	// (pane-border-status/-format) for the session. Nil means off, matching
	// tmux's own default.
	EnablePaneTitles *bool `toml:"enable_pane_titles,omitempty"`
	// PaneTitlePosition is "top" (default) or "bottom". Validated in
	// (*Config).validate.
	PaneTitlePosition string `toml:"pane_title_position,omitempty"`
	// PaneTitleFormat is a tmux format string; empty uses
	// "#{pane_index}: #{pane_current_command}".
	PaneTitleFormat string `toml:"pane_title_format,omitempty"`
}

// Window is one tmux window, laid out either by a split tree or a flat pane
// list (legacy format).
type Window struct {
	Name   string `toml:"name,omitempty"`
	Layout string `toml:"layout,omitempty"`
	// Root is this window's working directory. Relative paths resolve against
	// the session root, so a monorepo can say root = "api" and get a window
	// rooted there. Empty means the session root.
	//
	// Before this existed the only way to express it was pre_window = "cd api",
	// which types a visible cd into every pane of the window and races that
	// pane's own command.
	Root      string  `toml:"root,omitempty"`
	Splits    []Split `toml:"splits,omitempty"`
	Panes     []Pane  `toml:"panes,omitempty"`
	PreWindow string  `toml:"pre_window,omitempty"`
}

// Split is a node in a window's split tree.
type Split struct {
	Type string `toml:"type,omitempty"` // "", "h"/"horizontal", "v"/"vertical"
	Size int    `toml:"size,omitempty"` // percentage for the new pane; 0 = tmux default
	// Command is typed into the pane's shell, as if you had typed it.
	Command string `toml:"command,omitempty"`
	// Run makes the command the pane's own process instead of typing it into a
	// shell. There is no shell underneath, so the pane closes when it exits
	// (unless tmux's remain-on-exit is set) — which is exactly what you want for
	// a long-running server and exactly what you don't want for a prompt you
	// mean to keep using.
	//
	// It also sidesteps the two things typing can't do: the text never lands in
	// shell history, and a command starting with "#" is runnable (Command treats
	// those as comments).
	Run string `toml:"run,omitempty"`
	// Root overrides the window's directory for this pane and, unless they
	// override it themselves, its children.
	Root     string  `toml:"root,omitempty"`
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
	cfg, unknown, err := decode(data)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	// Not a silent fallback: cfg.dir is what a relative session.root resolves
	// against, so losing it doesn't degrade gracefully — it quietly roots the
	// session wherever the process happens to be standing, which is the bug
	// Config.dir exists to prevent.
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", path, err)
	}
	cfg.dir = filepath.Dir(abs)
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for _, key := range unknown {
		cfg.warnings = append(cfg.warnings, fmt.Sprintf(
			"unknown key %s — it is ignored (a typo?)", key))
	}
	if filepath.Base(path) == LegacyFileName {
		cfg.warnings = append(cfg.warnings, fmt.Sprintf(
			"%s is deprecated and will be removed in 1.0; rename it to %s", LegacyFileName, DefaultFileName))
	}
	return cfg, nil
}

// decode parses TOML into a Config and reports any keys the file sets that
// Config has no field for.
//
// Unknown keys are collected rather than rejected. A misspelled key is silently
// dropped by a plain Unmarshal, so `wyrm validate` would bless a config whose
// every key was a typo — the exact mistake validate exists to catch. But
// erroring outright would break configs that already carry stray keys, so for
// now they surface through Warnings (which `wyrm validate -strict` turns into a
// non-zero exit) and become errors in 1.0, alongside the other deprecations.
//
// go-toml still fills the destination when it reports strict errors, so the
// returned Config is complete either way.
func decode(data []byte) (*Config, []string, error) {
	var cfg Config
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		var missing *toml.StrictMissingError
		if !errors.As(err, &missing) {
			return nil, nil, err
		}
		return &cfg, UnknownKeys(missing), nil
	}
	return &cfg, nil, nil
}

// UnknownKeys renders the dotted key paths go-toml rejected under
// DisallowUnknownFields, quoted, for an error or warning message. Shared with
// the TUI's theme loader so the two report a typo the same way.
func UnknownKeys(e *toml.StrictMissingError) []string {
	keys := make([]string, 0, len(e.Errors))
	for _, d := range e.Errors {
		keys = append(keys, strconv.Quote(strings.Join(d.Key(), ".")))
	}
	return keys
}

// LoadDefault parses and validates the built-in fallback config, used when no
// config file is found in the current directory (see Discover).
func LoadDefault() (*Config, error) {
	// Strict, and an outright error rather than a warning: the built-in config
	// is ours, so an unknown key in it is a wyrm bug, not a user's typo.
	cfg, unknown, err := decode(defaultConfigData)
	if err != nil {
		return nil, fmt.Errorf("parsing default config: %w", err)
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("default config: unknown key %s", strings.Join(unknown, ", "))
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("default config: %w", err)
	}
	return cfg, nil
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
	switch c.Session.PaneTitlePosition {
	case "", "top", "bottom":
	default:
		return fmt.Errorf("session.pane_title_position must be %q or %q, got %q", "top", "bottom", c.Session.PaneTitlePosition)
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
		// Refusing rather than warning: the two do materially different things
		// (a pane with a shell under it, or without one), and silently picking
		// either would surprise half the people who wrote both.
		if s.Command != "" && s.Run != "" {
			return fmt.Errorf("window %q split %d: set command or run, not both (command types into a shell; run replaces it)", window, i)
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
		if wtName, ok := mainRepoWorktreeName(absRoot); ok {
			name = wtName
		} else {
			name = filepath.Base(absRoot)
		}
	}
	return name, absRoot, nil
}

// ResolveRoot resolves a configured directory against a base: "" yields base
// unchanged, an absolute (or ~/$VAR-expanded absolute) path is taken as-is, and
// a relative one is joined onto base. It is what makes a window's root = "api"
// mean "api inside the session root" while root = "~/other" still escapes it.
func ResolveRoot(base, root string) (string, error) {
	if root == "" {
		return base, nil
	}
	expanded, err := ExpandPath(root)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(expanded) {
		return filepath.Clean(expanded), nil
	}
	return filepath.Join(base, expanded), nil
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
