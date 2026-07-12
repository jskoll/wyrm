// Command wyrm creates repeatable tmux session layouts from a TOML config.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

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
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *showVersion {
		fmt.Fprintln(stdout, "wyrm "+version)
		return 0
	}

	if *pick {
		return runPicker(runner, stderr, insideTmux, attach)
	}

	path := *configPath
	var cfg *config.Config
	if path == "" {
		discovered, err := config.Discover()
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
				fmt.Fprintln(stderr, "wyrm: "+err.Error())
				return 1
			}
		} else {
			path = discovered
		}
	}
	if cfg == nil {
		var err error
		if cfg, err = config.Load(path); err != nil {
			fmt.Fprintln(stderr, "wyrm: "+err.Error())
			return 1
		}
	}

	if *kill {
		name, err := session.Kill(runner, cfg)
		if err != nil {
			fmt.Fprintln(stderr, "wyrm: "+err.Error())
			return 1
		}
		fmt.Fprintf(stdout, "killed session %s\n", name)
		return 0
	}

	name, created, err := session.Create(runner, cfg)
	if err != nil {
		fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	if created {
		fmt.Fprintf(stdout, "created session %s\n", name)
	} else {
		fmt.Fprintf(stdout, "session %s already running, attaching\n", name)
	}

	return attachOrSwitch(runner, stderr, insideTmux, attach, name)
}

// runPicker lets the user choose a running session and attaches to it. An
// empty choice (nothing running, or the user aborted) exits quietly.
func runPicker(runner tmux.Runner, stderr io.Writer, insideTmux func() bool, attach func(string) error) int {
	name, err := picker.Run(runner)
	if err != nil {
		fmt.Fprintln(stderr, "wyrm: "+err.Error())
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
			fmt.Fprintf(stderr, "wyrm: switching to session: %v (%s)\n", err, out)
			return 1
		}
		return 0
	}

	if err := attach(name); err != nil {
		fmt.Fprintf(stderr, "wyrm: attaching to session: %v\n", err)
		return 1
	}
	return 0
}
