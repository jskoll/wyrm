// Command wyrm creates repeatable tmux session layouts from a TOML config.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jskoll/wyrm/internal/config"
	"github.com/jskoll/wyrm/internal/freeze"
	"github.com/jskoll/wyrm/internal/picker"
	"github.com/jskoll/wyrm/internal/session"
	"github.com/jskoll/wyrm/internal/tmux"
	"github.com/pelletier/go-toml/v2"
)

// version is stamped at release time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, tmux.Exec{}, tmux.InsideTmux, tmux.Attach))
}

// run implements the CLI. It takes its dependencies as parameters (rather
// than reaching for globals like os.Stdout or the default flag.CommandLine)
// so tests can drive it without touching real stdio or a real tmux server.
func run(args []string, stdout, stderr io.Writer, runner tmux.Runner, insideTmux func() bool, attach func(string) error) int {
	fs := flag.NewFlagSet("wyrm", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to config file (default: .wyrm.toml, then .tmuxconfig)")
	kill := fs.Bool("kill", false, "kill the session (runs on_project_exit) instead of creating it")
	pick := fs.Bool("pick", false, "pick a running tmux session to attach to")
	showVersion := fs.Bool("version", false, "print version and exit")
	migrateConfig := fs.Bool("migrate-config", false, "move the local config into the shared config directory")
	validate := fs.Bool("validate", false, "check that the effective config parses and validates, without building a session")
	list := fs.Bool("list", false, "list running tmux sessions non-interactively")
	format := fs.String("format", "table", "output format for -list: table, json, toml, or names")
	edit := fs.Bool("edit", false, "open the resolved config in $EDITOR, creating one if none exists")
	listConfigs := fs.Bool("list-configs", false, "list candidate config file paths (for shell completion)")
	save := fs.Bool("save", false, "save the running session's current layout as this folder's config")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *showVersion {
		_, _ = fmt.Fprintln(stdout, "wyrm "+version)
		return 0
	}

	if fs.NArg() > 0 {
		return runAttachByName(runner, stderr, insideTmux, attach, fs.Arg(0))
	}

	settings, err := config.LoadSettings()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}

	if *migrateConfig {
		return runMigrateConfig(stdout, stderr, settings)
	}

	if *validate {
		return runValidate(stdout, stderr, settings, *configPath)
	}

	if *edit {
		return runEdit(stderr, settings, *configPath)
	}

	if *listConfigs {
		return runListConfigs(stdout, settings)
	}

	if *save {
		return runSave(runner, stdout, stderr, settings, insideTmux)
	}

	if *list {
		return runList(runner, stdout, stderr, *format)
	}

	if *pick {
		return runPicker(runner, stderr, insideTmux, attach)
	}

	path := *configPath
	var cfg *config.Config
	if path == "" {
		discovered, err := config.DiscoverGlobal(settings)
		if err != nil {
			// No config here: build the default session for this folder.
			// session.Create reattaches instead of rebuilding if a session
			// by that name is already running, so unrelated sessions running
			// elsewhere don't affect this folder's outcome.
			if cfg, err = config.LoadUserDefault(); err != nil {
				_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
				return 1
			}
			if cfg == nil {
				if cfg, err = config.LoadDefault(); err != nil {
					_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
					return 1
				}
			}
		} else {
			path = discovered
		}
	}
	if cfg == nil {
		var err error
		if cfg, err = config.Load(path); err != nil {
			_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
			return 1
		}
	}

	if *kill {
		name, err := session.Kill(runner, cfg, stderr)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "killed session %s\n", name)
		return 0
	}

	name, sessionID, created, err := session.Create(runner, cfg, stdout, stderr)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	if created {
		_, _ = fmt.Fprintf(stdout, "created session %s\n", name)
	} else {
		_, _ = fmt.Fprintf(stdout, "session %s already running, attaching\n", name)
	}

	return attachOrSwitch(runner, stderr, insideTmux, attach, sessionID)
}

// runAttachByName attaches or switches directly to the exact-named running
// session, without the interactive picker (-pick). This is what shell
// completion (see completions/) completes a bare positional argument to.
func runAttachByName(runner tmux.Runner, stderr io.Writer, insideTmux func() bool, attach func(string) error, name string) int {
	id, ok, err := tmux.FindSessionID(runner, name)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	if !ok {
		_, _ = fmt.Fprintf(stderr, "wyrm: no running session named %q\n", name)
		return 1
	}
	return attachOrSwitch(runner, stderr, insideTmux, attach, id)
}

// runListConfigs prints config paths wyrm knows about: the local file (if
// present) and every candidate in the shared config directory. These are
// the candidates shell completion offers for -config; -config itself can
// point at any of them regardless of the current storage setting.
func runListConfigs(stdout io.Writer, settings *config.Settings) int {
	for _, name := range []string{config.DefaultFileName, config.LegacyFileName} {
		if _, err := os.Stat(name); err == nil {
			_, _ = fmt.Fprintln(stdout, name)
		}
	}
	if dir, err := settings.ResolvedSharedDir(); err == nil {
		matches, _ := filepath.Glob(filepath.Join(dir, "*"+config.DefaultFileName))
		for _, m := range matches {
			_, _ = fmt.Fprintln(stdout, m)
		}
	}
	return 0
}

// runMigrateConfig moves the current directory's local config file into the
// shared config directory, named "<folderName>.wyrm.toml". It does not
// touch the storage setting itself; run this after (or before) switching
// settings.Storage to "shared".
func runMigrateConfig(stdout, stderr io.Writer, settings *config.Settings) int {
	src, err := config.Discover()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: no local config to migrate: "+err.Error())
		return 1
	}

	cwd, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	dst, err := settings.SharedConfigPath(cwd)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}

	if _, err := os.Stat(dst); err == nil {
		_, _ = fmt.Fprintf(stderr, "wyrm: %s already exists, remove it first\n", dst)
		return 1
	} else if !errors.Is(err, os.ErrNotExist) {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	if err := os.Rename(src, dst); err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "moved %s to %s\n", src, dst)
	if settings.Storage != config.StorageShared {
		settingsPath, err := config.SettingsPath()
		if err == nil {
			_, _ = fmt.Fprintf(stdout, "note: set storage = \"shared\" in %s for wyrm to use it\n", settingsPath)
		}
	}
	return 0
}

// runValidate checks that the effective config (the one wyrm would actually
// use) parses and validates, without building a session.
func runValidate(stdout, stderr io.Writer, settings *config.Settings, configPath string) int {
	_, source, err := config.ResolveEffective(settings, configPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "config valid: %s\n", source)
	return 0
}

// runEdit opens the resolved config in $EDITOR (falling back to vi), creating
// one at the location wyrm would look next time if none exists yet. After
// the editor exits, a saved-but-invalid file gets a warning rather than an
// error, matching the project's warn-don't-abort philosophy for anything
// that isn't a structural failure.
func runEdit(stderr io.Writer, settings *config.Settings, explicitPath string) int {
	path := explicitPath
	if path == "" {
		resolved, exists, err := config.EditTarget(settings)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
			return 1
		}
		path = resolved
		if !exists {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
				return 1
			}
		}
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	parts := strings.Fields(editor)
	if len(parts) == 0 {
		_, _ = fmt.Fprintln(stderr, "wyrm: $EDITOR is set but empty")
		return 1
	}
	cmd := exec.Command(parts[0], append(parts[1:], path)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	runErr := cmd.Run()

	if _, statErr := os.Stat(path); statErr == nil {
		if _, loadErr := config.Load(path); loadErr != nil {
			_, _ = fmt.Fprintf(stderr, "wyrm: warning: %s: %v\n", path, loadErr)
		}
	}

	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return exitErr.ExitCode()
		}
		_, _ = fmt.Fprintln(stderr, "wyrm: "+runErr.Error())
		return 1
	}
	return 0
}

// runSave snapshots a running session's windows, split layout, and
// foreground pane commands into a new config for the current folder (see
// internal/freeze). The target session is the one wyrm is currently
// attached to when run from inside tmux, or the folder's own session
// (looked up the same way a bare `wyrm` would resolve its name) otherwise.
// Like -migrate-config, it refuses to overwrite an existing config rather
// than silently discarding hand-written hooks or comments.
func runSave(runner tmux.Runner, stdout, stderr io.Writer, settings *config.Settings, insideTmux func() bool) int {
	var sessionID, sessionName string
	if insideTmux() {
		id, name, err := tmux.CurrentSession(runner)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
			return 1
		}
		sessionID, sessionName = id, name
	} else {
		cfg, _, err := config.ResolveEffective(settings, "")
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
			return 1
		}
		name, _, err := cfg.Session.Resolve()
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
			return 1
		}
		id, ok, err := tmux.FindSessionID(runner, name)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
			return 1
		}
		if !ok {
			_, _ = fmt.Fprintf(stderr, "wyrm: no running session named %q for this folder (run it from inside the session you want to save, or start it with wyrm first)\n", name)
			return 1
		}
		sessionID, sessionName = id, name
	}

	dest, exists, err := config.EditTarget(settings)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	if exists {
		_, _ = fmt.Fprintf(stderr, "wyrm: %s already exists, remove it first\n", dest)
		return 1
	}

	cfg, err := freeze.Config(runner, sessionID, sessionName, ".")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}

	if _, loadErr := config.Load(dest); loadErr != nil {
		_, _ = fmt.Fprintf(stderr, "wyrm: warning: %s: %v\n", dest, loadErr)
	}

	_, _ = fmt.Fprintf(stdout, "saved session %s to %s\n", sessionName, dest)
	return 0
}

// runList prints the running tmux sessions non-interactively, in the given
// format — for scripts and status bars, where the interactive -pick UI
// doesn't apply. An empty session list is not an error in any format: table
// mode reports it on stderr (matching picker.Run's message) but exits 0;
// json/toml print an empty array so consumers don't need to special-case
// "no server running".
func runList(runner tmux.Runner, stdout, stderr io.Writer, format string) int {
	sessions, err := picker.ListSessions(runner)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	if sessions == nil {
		sessions = []picker.Session{}
	}

	switch format {
	case "table":
		if len(sessions) == 0 {
			_, _ = fmt.Fprintln(stderr, "wyrm: no running tmux sessions")
			return 0
		}
		for _, s := range sessions {
			_, _ = fmt.Fprintln(stdout, formatSessionRow(s))
		}
	case "names":
		for _, s := range sessions {
			_, _ = fmt.Fprintln(stdout, s.Name)
		}
	case "json":
		data, err := json.MarshalIndent(sessions, "", "  ")
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
			return 1
		}
		_, _ = fmt.Fprintln(stdout, string(data))
	case "toml":
		data, err := toml.Marshal(struct {
			Sessions []picker.Session `toml:"sessions"`
		}{Sessions: sessions})
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
			return 1
		}
		_, _ = stdout.Write(data)
	default:
		_, _ = fmt.Fprintf(stderr, "wyrm: unknown -format %q (use table, json, toml, or names)\n", format)
		return 1
	}
	return 0
}

// formatSessionRow renders one session as a plain, awk-able line: name,
// window count, and an attached marker — the same shape as the picker's row,
// minus color codes. See picker.FormatRow.
func formatSessionRow(s picker.Session) string {
	return picker.FormatRow(s, false)
}

// runPicker lets the user choose a running session and attaches to it. An
// empty choice (nothing running, or the user aborted) exits quietly.
func runPicker(runner tmux.Runner, stderr io.Writer, insideTmux func() bool, attach func(string) error) int {
	sessionID, err := picker.Run(runner, stderr)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	if sessionID == "" {
		return 0
	}
	return attachOrSwitch(runner, stderr, insideTmux, attach, sessionID)
}

// attachOrSwitch hands the terminal to the session identified by sessionID
// (a tmux session ID such as "$3" — see tmux.FindSessionID for why a raw
// session name isn't used here), switching the client instead of nesting
// when wyrm is already running inside tmux.
func attachOrSwitch(runner tmux.Runner, stderr io.Writer, insideTmux func() bool, attach func(string) error, sessionID string) int {
	if insideTmux() {
		if out, err := runner.Run("switch-client", "-t", sessionID); err != nil {
			_, _ = fmt.Fprintf(stderr, "wyrm: switching to session: %v (%s)\n", err, out)
			return 1
		}
		return 0
	}

	if err := attach(sessionID); err != nil {
		_, _ = fmt.Fprintf(stderr, "wyrm: attaching to session: %v\n", err)
		return 1
	}
	return 0
}
