package session_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jskoll/wyrm/internal/config"
	"github.com/jskoll/wyrm/internal/session"
	"github.com/jskoll/wyrm/internal/tmux"
)

// TestIntegration drives a real tmux server on an isolated socket: creates a
// session with both layout formats, checks the resulting windows/panes and
// lifecycle hooks, then kills it. Skipped with -short or without tmux.
func TestIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	r := tmux.Exec{SocketName: fmt.Sprintf("wyrm-it-%d", os.Getpid())}
	t.Cleanup(func() { r.Run("kill-server") }) //nolint:errcheck

	root := t.TempDir()
	cfg := &config.Config{
		Session: config.Session{
			Name:           "wyrm-it",
			Root:           root,
			OnProjectStart: "touch started",
			OnProjectExit:  "touch exited",
			StartupWindow:  "code",
		},
		Windows: []config.Window{
			{Name: "code", Splits: []config.Split{
				{Command: "# editor placeholder"},
				{Type: "h", Size: 30, Children: []config.Split{{Type: "v"}}},
			}},
			{Name: "misc", Layout: "even-horizontal", Panes: []config.Pane{
				{Command: "# a"}, {Command: "# b"},
			}},
		},
	}

	name, created, err := session.Create(r, cfg)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if name != "wyrm-it" {
		t.Fatalf("name = %q, want wyrm-it", name)
	}
	if !created {
		t.Error("created = false, want true for a fresh session")
	}

	if _, err := os.Stat(filepath.Join(root, "started")); err != nil {
		t.Error("on_project_start hook did not run in the session root")
	}

	out, err := r.Run("list-windows", "-t", name, "-F", "#{window_name}|#{window_panes}|#{window_active}")
	if err != nil {
		t.Fatalf("list-windows: %v (%s)", err, out)
	}
	windows := map[string]string{}
	activeWindow := ""
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(line, "|")
		if len(parts) != 3 {
			t.Fatalf("unexpected list-windows line %q", line)
		}
		windows[parts[0]] = parts[1]
		if parts[2] == "1" {
			activeWindow = parts[0]
		}
	}
	if got := windows["code"]; got != "3" {
		t.Errorf("window code has %s panes, want 3 (initial + split + nested child)", got)
	}
	if got := windows["misc"]; got != "2" {
		t.Errorf("window misc has %s panes, want 2", got)
	}
	if activeWindow != "code" {
		t.Errorf("active window = %q, want startup_window code", activeWindow)
	}

	if _, created, err := session.Create(r, cfg); err != nil {
		t.Fatalf("Create (second call): %v", err)
	} else if created {
		t.Error("created = true on second Create, want false for an already-running session")
	}
	out, err = r.Run("list-windows", "-t", name, "-F", "#{window_name}")
	if err != nil {
		t.Fatalf("list-windows after reattach: %v (%s)", err, out)
	}
	if got := strings.Count(out, "\n") + 1; got != 2 {
		t.Errorf("window count after reattach = %d, want 2 (session was rebuilt instead of reattached)", got)
	}

	if _, err := session.Kill(r, cfg); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "exited")); err != nil {
		t.Error("on_project_exit hook did not run in the session root")
	}
	if _, err := r.Run("has-session", "-t", name); err == nil {
		t.Error("session still exists after Kill")
	}
}
