package main

// Verbs that show running sessions: the interactive picker, the full-screen
// TUI, and the non-interactive list the other two exist to replace for scripts.

import (
	"encoding/json"
	"fmt"

	"github.com/jskoll/wyrm/internal/config"
	"github.com/jskoll/wyrm/internal/picker"
	"github.com/jskoll/wyrm/internal/tui"
	"github.com/pelletier/go-toml/v2"
)

// list prints the running tmux sessions non-interactively, in the given
// format — for scripts and status bars, where the interactive picker doesn't
// apply. An empty session list is not an error in any format: table mode
// reports it on stderr (matching picker.Run's message) but exits 0; json/toml
// print an empty array so consumers don't need to special-case "no server
// running".
func (a *app) list(args []string) error {
	fs := a.newFlagSet("list")
	format := fs.String("format", "table", "output format: table, json, toml, or names")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if err := requireNoArgs(fs); err != nil {
		return err
	}

	sessions, err := picker.ListSessions(a.runner)
	if err != nil {
		return err
	}
	if sessions == nil {
		sessions = []picker.Session{}
	}

	switch *format {
	case "table":
		if len(sessions) == 0 {
			_, _ = fmt.Fprintln(a.stderr, "wyrm: no running tmux sessions")
			return nil
		}
		for _, s := range sessions {
			_, _ = fmt.Fprintln(a.stdout, formatSessionRow(s))
		}
	case "names":
		for _, s := range sessions {
			_, _ = fmt.Fprintln(a.stdout, s.Name)
		}
	case "json":
		data, err := json.MarshalIndent(sessions, "", "  ")
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(a.stdout, string(data))
	case "toml":
		data, err := toml.Marshal(struct {
			Sessions []picker.Session `toml:"sessions"`
		}{Sessions: sessions})
		if err != nil {
			return err
		}
		_, _ = a.stdout.Write(data)
	default:
		// Exit 2, like any other bad flag value — an unknown -format is a usage
		// error, not a runtime failure.
		return usageErrf("unknown -format %q (use table, json, toml, or names)", *format)
	}
	return nil
}

// formatSessionRow renders one session as a plain, awk-able line: name,
// window count, and an attached marker — the same shape as the picker's row,
// minus color codes. See picker.FormatRow.
func formatSessionRow(s picker.Session) string {
	return picker.FormatRow(s, false)
}

// pick lets the user choose a running session and attaches to it. An empty
// choice (nothing running, or the user aborted) exits quietly.
func (a *app) pick(args []string) error {
	fs := a.newFlagSet("pick")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if err := requireNoArgs(fs); err != nil {
		return err
	}
	sessionID, err := picker.Run(a.runner, a.stderr)
	if err != nil {
		return err
	}
	if sessionID == "" {
		return nil
	}
	return a.attachOrSwitch(sessionID)
}

// tui opens the interactive session-management TUI and, if the user chose a
// session to attach to, hands the terminal over after the alt-screen program
// exits — the same deferred-attach dance pick uses, since a full-screen
// program can't attach in place.
func (a *app) tui(args []string) error {
	fs := a.newFlagSet("tui")
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
	sessionID, err := tui.Run(a.runner, settings, a.stderr)
	if err != nil {
		return err
	}
	if sessionID == "" {
		return nil
	}
	return a.attachOrSwitch(sessionID)
}
