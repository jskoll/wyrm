// Package tmux wraps execution of tmux commands.
package tmux

import (
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

// Attach hands the caller's terminal to a tmux client attached to session.
// It is not part of Runner because it needs the process's stdio.
func Attach(session string) error {
	cmd := exec.Command("tmux", "attach-session", "-t", session)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
