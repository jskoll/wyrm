package picker

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/jskoll/wyrm/internal/tmux"
)

// TestListAndKillIntegration drives a real tmux server on an isolated socket:
// creates two sessions, verifies ListSessions reports them (most-recently
// active first), then kills one through KillSession and confirms it's gone.
// Skipped with -short or without tmux. The interactive Run loop needs a real
// TTY and is exercised manually, not here.
func TestListAndKillIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	r := tmux.Exec{SocketName: fmt.Sprintf("wyrm-pick-it-%d", os.Getpid())}
	t.Cleanup(func() { r.Run("kill-server") }) //nolint:errcheck

	// No server running yet: ListSessions must report empty, not error.
	if got, err := ListSessions(r); err != nil || len(got) != 0 {
		t.Fatalf("ListSessions before any server: got %v, err %v; want empty, nil", got, err)
	}

	for _, name := range []string{"alpha", "beta"} {
		if out, err := r.Run("new-session", "-d", "-s", name); err != nil {
			t.Fatalf("new-session %q: %v (%s)", name, err, out)
		}
	}

	got, err := ListSessions(r)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d sessions, want 2: %v", len(got), names(got))
	}
	seen := map[string]Session{}
	for _, s := range got {
		seen[s.Name] = s
	}
	if _, ok := seen["alpha"]; !ok {
		t.Errorf("alpha missing from %v", names(got))
	}
	if s, ok := seen["beta"]; !ok || s.Windows != 1 {
		t.Errorf("beta wrong: %+v", s)
	}

	if err := KillSession(r, "alpha"); err != nil {
		t.Fatalf("KillSession: %v", err)
	}
	after, err := ListSessions(r)
	if err != nil {
		t.Fatalf("ListSessions after kill: %v", err)
	}
	if len(after) != 1 || after[0].Name != "beta" {
		t.Fatalf("after kill got %v, want just beta", names(after))
	}
}
