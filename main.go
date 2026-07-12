// Command wyrm creates repeatable tmux session layouts from a TOML config.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jskoll/wyrm/internal/config"
	"github.com/jskoll/wyrm/internal/picker"
	"github.com/jskoll/wyrm/internal/session"
	"github.com/jskoll/wyrm/internal/tmux"
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
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *showVersion {
		_, _ = fmt.Fprintln(stdout, "wyrm "+version)
		return 0
	}

	settings, err := config.LoadSettings()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}

	if *migrateConfig {
		return runMigrateConfig(stdout, stderr, settings)
	}

	if *pick {
		return runPicker(runner, stderr, insideTmux, attach)
	}

	path := *configPath
	var cfg *config.Config
	if path == "" {
		discovered, err := config.DiscoverGlobal(settings)
		if err != nil {
			// No config here: if sessions are already running, offer to pick
			// one instead of silently building the default session. -kill is
			// exempt — it targets the default-config session, not the picker.
			if !*kill {
				if sessions, lerr := picker.ListSessions(runner); lerr == nil && len(sessions) > 0 {
					return runPicker(runner, stderr, insideTmux, attach)
				}
			}
			if cfg, err = config.LoadDefault(); err != nil {
				_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
				return 1
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
		name, err := session.Kill(runner, cfg)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "killed session %s\n", name)
		return 0
	}

	name, created, err := session.Create(runner, cfg)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	if created {
		_, _ = fmt.Fprintf(stdout, "created session %s\n", name)
	} else {
		_, _ = fmt.Fprintf(stdout, "session %s already running, attaching\n", name)
	}

	return attachOrSwitch(runner, stderr, insideTmux, attach, name)
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

// runPicker lets the user choose a running session and attaches to it. An
// empty choice (nothing running, or the user aborted) exits quietly.
func runPicker(runner tmux.Runner, stderr io.Writer, insideTmux func() bool, attach func(string) error) int {
	name, err := picker.Run(runner)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	if name == "" {
		return 0
	}
	return attachOrSwitch(runner, stderr, insideTmux, attach, name)
}

// attachOrSwitch hands the terminal to the session, switching the client
// instead of nesting when wyrm is already running inside tmux.
func attachOrSwitch(runner tmux.Runner, stderr io.Writer, insideTmux func() bool, attach func(string) error, name string) int {
	if insideTmux() {
		if out, err := runner.Run("switch-client", "-t", name); err != nil {
			_, _ = fmt.Fprintf(stderr, "wyrm: switching to session: %v (%s)\n", err, out)
			return 1
		}
		return 0
	}

	if err := attach(name); err != nil {
		_, _ = fmt.Fprintf(stderr, "wyrm: attaching to session: %v\n", err)
		return 1
	}
	return 0
}
