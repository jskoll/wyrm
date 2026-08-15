package main

// Verbs that act on config files rather than on running sessions: edit,
// validate, save, migrate-config, list-configs.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/jskoll/wyrm/internal/config"
	"github.com/jskoll/wyrm/internal/editor"
	"github.com/jskoll/wyrm/internal/freeze"
	"github.com/jskoll/wyrm/internal/tmux"
	"github.com/pelletier/go-toml/v2"
)

// listConfigs prints config paths wyrm knows about: the local file (if
// present), every candidate in the shared config directory, and every
// [[wildcard]] template that currently matches at least one directory.
// These are the candidates shell completion offers for -config; -config
// itself can point at any of them regardless of the current storage setting.
func (a *app) listConfigs(args []string) error {
	fs := a.newFlagSet("list-configs")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if err := requireNoArgs(fs); err != nil {
		return err
	}
	settings, err := config.LoadSettings()
	if err != nil {
		return err
	}
	for _, name := range []string{config.DefaultFileName, config.LegacyFileName} {
		if _, err := os.Stat(name); err == nil {
			_, _ = fmt.Fprintln(a.stdout, name)
		}
	}
	if dir, err := settings.ResolvedSharedDir(); err == nil {
		matches, _ := filepath.Glob(filepath.Join(dir, "*"+config.DefaultFileName))
		for _, m := range matches {
			_, _ = fmt.Fprintln(a.stdout, m)
		}
	}
	seen := map[string]bool{}
	for _, p := range config.DiscoverWildcardProjects(settings) {
		if !seen[p.Path] {
			seen[p.Path] = true
			_, _ = fmt.Fprintln(a.stdout, p.Path)
		}
	}
	return nil
}

// migrateConfig moves the current directory's local config file into the
// shared config directory, named "<folderName>.wyrm.toml". It does not
// touch the storage setting itself; run this after (or before) switching
// settings.Storage to "shared".
func (a *app) migrateConfig(args []string) error {
	fs := a.newFlagSet("migrate-config")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if err := requireNoArgs(fs); err != nil {
		return err
	}
	settings, err := config.LoadSettings()
	if err != nil {
		return err
	}

	src, err := config.Discover()
	if err != nil {
		return fmt.Errorf("no local config to migrate: %w", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	dst, err := settings.SharedConfigPath(cwd)
	if err != nil {
		return err
	}

	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("%s already exists, remove it first", dst)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(a.stdout, "moved %s to %s\n", src, dst)
	if settings.Storage != config.StorageShared {
		if settingsPath, err := config.SettingsPath(); err == nil {
			_, _ = fmt.Fprintf(a.stdout, "note: set storage = \"shared\" in %s for wyrm to use it\n", settingsPath)
		}
	}
	return nil
}

// validate checks that the effective config (the one wyrm would actually
// use) parses and validates, without building a session.
func (a *app) validate(args []string) error {
	fs := a.newFlagSet("validate")
	configPath := fs.String("config", "", "path to config file (default: .wyrm.toml, then .tmuxconfig)")
	strict := fs.Bool("strict", false, "exit non-zero if the config has warnings (typos, deprecations)")
	var vars varMapFlag
	fs.Var(&vars, "var", "set template variable (KEY=VALUE, can be repeated)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if err := requireNoArgs(fs); err != nil {
		return err
	}
	settings, err := config.LoadSettings()
	if err != nil {
		return err
	}
	cfg, source, err := config.ResolveEffective(settings, *configPath)
	if err != nil {
		return err
	}
	if len(vars) > 0 {
		cfg.Interpolate(vars)
	}
	a.printWarnings(cfg)
	// Warnings are not failures by default — a deprecated `panes` list still
	// builds the session its author wanted. -strict is for CI, where "this
	// config has a typo in it" should stop the build.
	if *strict && len(cfg.Warnings()) > 0 {
		return fmt.Errorf("%s has %d warning(s) and -strict was given", source, len(cfg.Warnings()))
	}
	_, _ = fmt.Fprintf(a.stdout, "config valid: %s\n", source)
	return nil
}

// edit opens the resolved config in $EDITOR (falling back to vi), creating
// one at the location wyrm would look next time if none exists yet. After
// the editor exits, a saved-but-invalid file gets a warning rather than an
// error, matching the project's warn-don't-abort philosophy for anything
// that isn't a structural failure.
func (a *app) edit(args []string) error {
	fs := a.newFlagSet("edit")
	explicitPath := fs.String("config", "", "path to config file (default: the resolved config)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	settings, err := config.LoadSettings()
	if err != nil {
		return err
	}

	path := *explicitPath
	if path == "" {
		resolved, _, err := config.EditTarget(settings)
		if err != nil {
			return err
		}
		path = resolved
	}
	// Create the parent directory whichever way the path was arrived at:
	// `wyrm edit -config new/dir/x.toml` used to hand the editor a path it
	// couldn't write, while the flagless form worked.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	cmd, err := editor.Command(path)
	if err != nil {
		return err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	runErr := cmd.Run()

	if _, statErr := os.Stat(path); statErr == nil {
		if _, loadErr := config.Load(path); loadErr != nil {
			_, _ = fmt.Fprintf(a.stderr, "wyrm: warning: %s: %v\n", path, loadErr)
		}
	}

	if runErr != nil {
		var exitError *exec.ExitError
		if errors.As(runErr, &exitError) {
			// ExitCode is -1 when the editor was killed by a signal, and
			// os.Exit(-1) reaches the shell as 255. Report the conventional
			// 128+signal instead. Either way the editor has already said
			// whatever it wanted to, so nothing more is printed.
			if code := exitError.ExitCode(); code >= 0 {
				return silent(code)
			}
			return silent(1)
		}
		return runErr
	}
	return nil
}

// save snapshots a running session's windows, split layout, and foreground
// pane commands into a new config for the current folder (see internal/freeze).
// The target session is the one wyrm is currently attached to when run from
// inside tmux, or the folder's own session (looked up the same way a bare
// `wyrm` would resolve its name) otherwise. Like migrate-config, it refuses to
// overwrite an existing config rather than silently discarding hand-written
// hooks or comments.
func (a *app) save(args []string) error {
	fs := a.newFlagSet("save")
	configPath := fs.String("config", "", "path to write the saved config (default: the discovered/shared location)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	settings, err := config.LoadSettings()
	if err != nil {
		return err
	}

	sessionID, sessionName, err := a.saveTarget(settings)
	if err != nil {
		return err
	}

	dest, err := a.saveDestination(settings, *configPath)
	if err != nil {
		return err
	}

	cfg, err := freeze.Config(a.runner, sessionID, sessionName, a.saveRoot(sessionID, dest))
	if err != nil {
		return err
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return err
	}

	if _, loadErr := config.Load(dest); loadErr != nil {
		_, _ = fmt.Fprintf(a.stderr, "wyrm: warning: %s: %v\n", dest, loadErr)
	}

	_, _ = fmt.Fprintf(a.stdout, "saved session %s to %s\n", sessionName, dest)
	return nil
}

// saveTarget picks the session to snapshot: the one wyrm is attached to when
// run inside tmux, else the current folder's own.
func (a *app) saveTarget(settings *config.Settings) (sessionID, sessionName string, err error) {
	if a.insideTmux() {
		return tmux.CurrentSession(a.runner)
	}
	cfg, _, err := config.ResolveEffective(settings, "")
	if err != nil {
		return "", "", err
	}
	name, _, err := cfg.Session.Resolve(cfg.Dir())
	if err != nil {
		return "", "", err
	}
	id, ok, err := tmux.FindSessionID(a.runner, name)
	if err != nil {
		return "", "", err
	}
	if !ok {
		return "", "", fmt.Errorf(
			"no running session named %q for this folder (run it from inside the session you want to save, or start it with wyrm first)", name)
	}
	return id, name, nil
}

// saveDestination resolves where the snapshot is written, refusing to clobber
// an existing config.
func (a *app) saveDestination(settings *config.Settings, explicit string) (string, error) {
	dest, exists := explicit, false
	if dest == "" {
		var err error
		dest, exists, err = config.EditTarget(settings)
		if err != nil {
			return "", err
		}
	} else if _, statErr := os.Stat(dest); statErr == nil {
		exists = true
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", statErr
	}
	if exists {
		return "", fmt.Errorf("%s already exists, remove it first", dest)
	}
	return dest, nil
}

// saveRoot decides what to write as the saved config's session.root.
//
// "." is preferred, because it keeps the config portable — a repo can commit
// its .wyrm.toml and every clone works. But "." is only correct when the
// session's own directory is the one the config is being written next to;
// saving session "api" into ~/web, or into the shared config directory, would
// otherwise produce a config that rebuilds the layout in the wrong place. In
// those cases write the session's real path and say so.
func (a *app) saveRoot(sessionID, dest string) string {
	path, err := tmux.SessionPath(a.runner, sessionID)
	if err != nil || path == "" {
		return "."
	}
	destDir, err := filepath.Abs(filepath.Dir(dest))
	if err != nil {
		return path
	}
	if absPath, err := filepath.Abs(path); err == nil && absPath == destDir {
		return "."
	}
	_, _ = fmt.Fprintf(a.stderr,
		"wyrm: warning: the session's directory (%s) is not where this config is being written (%s); saving an absolute session.root\n",
		path, destDir)
	return path
}
