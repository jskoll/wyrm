// Package tmux wraps execution of tmux commands.
package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Runner executes a tmux command and returns its combined output, trimmed.
// Tests substitute a recording mock to assert command sequences without a
// tmux server.
type Runner interface {
	Run(args ...string) (string, error)
}

// Exec is the real Runner, shelling out to the tmux binary on PATH.
type Exec struct {
	// SocketName selects a separate tmux server (tmux -L). Empty uses the
	// default server. Used by integration tests to stay isolated.
	SocketName string
}

// Run implements Runner.
func (e Exec) Run(args ...string) (string, error) {
	if e.SocketName != "" {
		args = append([]string{"-L", e.SocketName}, args...)
	}
	out, err := exec.Command("tmux", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// InsideTmux reports whether the current process runs inside a tmux client.
func InsideTmux() bool {
	return os.Getenv("TMUX") != ""
}

// Attach hands the caller's terminal to a tmux client attached to target,
// which should be a tmux session ID (e.g. "$3") rather than a session name —
// see FindSessionID for why. It is not part of Runner because it needs the
// process's stdio.
func Attach(target string) error {
	cmd := exec.Command("tmux", "attach-session", "-t", target)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// CurrentSession returns the ID and name of the tmux session wyrm is
// running inside (i.e. $TMUX is set). It's meaningless to call this outside
// tmux — callers should check InsideTmux first.
func CurrentSession(r Runner) (id, name string, err error) {
	out, err := r.Run("display-message", "-p", "-F", "#{session_id}|#{session_name}")
	if err != nil {
		return "", "", fmt.Errorf("finding current session: %v (%s)", err, out)
	}
	id, name, ok := strings.Cut(out, "|")
	if !ok {
		return "", "", fmt.Errorf("unexpected tmux output %q", out)
	}
	return id, name, nil
}

// FindSessionID returns the tmux session ID for the exact session name, and
// whether a matching session exists. It lists every session and compares
// names in Go rather than passing the name through tmux's -t target syntax:
// that syntax treats "." as the window.pane separator, so a session name
// containing "." (e.g. "wyrm.vim") is misparsed by commands like
// has-session, kill-session, new-window, or attach-session — even with an
// "=" exact-match prefix, which only guards against prefix ambiguity, not
// this. Once found, the returned ID is a safe, unambiguous target for any
// command regardless of what characters the session's name contains.
func FindSessionID(r Runner, name string) (id string, ok bool, err error) {
	out, err := r.Run("list-sessions", "-F", "#{session_id}|#{session_name}")
	if err != nil {
		// With no server up, tmux fails rather than printing an empty list.
		// The wording varies: "no server running on <socket>" for the default
		// server, "error connecting to <socket> (No such file or directory)"
		// for an -L socket that was never created. Treat both as "no such
		// session" rather than an error.
		msg := strings.ToLower(out)
		if strings.Contains(msg, "no server running") || strings.Contains(msg, "error connecting") {
			return "", false, nil
		}
		return "", false, fmt.Errorf("listing sessions: %v (%s)", err, out)
	}
	for _, line := range strings.Split(out, "\n") {
		sessID, sessName, found := strings.Cut(strings.TrimRight(line, "\r"), "|")
		if !found {
			continue
		}
		if sessName == name {
			return sessID, true, nil
		}
	}
	return "", false, nil
}
