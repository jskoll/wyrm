// Package editor resolves which editor wyrm should hand a config file to,
// shared by `wyrm edit` and the TUI's edit-config action so the two can't
// drift apart.
package editor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
)

// probeTimeout caps the login-shell probe. Starting an interactive shell runs
// the user's whole rc — plugin managers, version managers, prompt setup — and
// a wedged one must not take the TUI down with it.
const probeTimeout = 3 * time.Second

// Fallback is the editor used when no preference can be discovered anywhere.
const Fallback = "vi"

// lookupShellEditor is the login-shell probe, indirected for tests.
var lookupShellEditor = shellEditor

// Resolve returns the editor command line, split into the program and any
// arguments it was configured with ("code -w" and the like).
//
// $EDITOR wins, but its absence doesn't mean the user has no preference:
// wyrm usually runs under tmux, and a pane spawned by the tmux server (a
// popup, or anything the wyrm.nvim plugin launches) inherits the server's
// environment, which is whatever the server was started with rather than
// whatever the user's rc files export. Rather than drop such a user into vi,
// ask their login shell what $EDITOR is before giving up.
func Resolve() ([]string, error) {
	value := os.Getenv("EDITOR")
	if value == "" {
		value = lookupShellEditor()
	}
	if value == "" {
		return []string{Fallback}, nil
	}
	parts := strings.Fields(value)
	if len(parts) == 0 {
		return nil, errors.New("$EDITOR is set but empty")
	}
	return parts, nil
}

// Command builds the process that opens path in the resolved editor. Callers
// wire up its I/O themselves: `wyrm edit` inherits the terminal directly,
// while the TUI hands the command to tea.ExecProcess.
func Command(path string) (*exec.Cmd, error) {
	parts, err := Resolve()
	if err != nil {
		return nil, err
	}
	return exec.Command(parts[0], append(parts[1:], path)...), nil
}

// shellEditor asks $SHELL to print its $EDITOR. The shell is run login *and*
// interactive because that's where the export usually lives — ~/.zshrc is
// only read by interactive shells — with stdin detached so an interactive
// zsh doesn't start ZLE and fight the TUI for the terminal, and stderr
// discarded so a chatty rc can't corrupt the display.
func shellEditor() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, shell, "-lic", `printf %s "$EDITOR"`)
	cmd.Stdin = nil
	cmd.Stderr = nil
	out, err := cmd.Output()
	if err != nil {
		// Shells that don't take -lic (fish's spelling differs, csh has no
		// -c-with-login) land here. There's nothing better to try, so let
		// the caller fall back.
		return ""
	}
	return strings.TrimSpace(string(out))
}
