package session_test

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/jskoll/wyrm/internal/config"
	"github.com/jskoll/wyrm/internal/freeze"
	"github.com/jskoll/wyrm/internal/session"
	"github.com/jskoll/wyrm/internal/tmux"
)

// TestIntegrationNestedSplitGeometry drives a real tmux server and checks the
// *shape* of the result, not just the pane count.
//
// The layout below is the case applySplits used to get wrong: a nested
// container that is not the last sibling. Children were created before the
// next sibling, so that sibling split a pane its own children had already
// subdivided — the left column ended up 24 rows tall instead of the full
// window, and the right-hand pane took the wrong share. Siblings are now
// created breadth-first, so `size` means "share of this container" and a
// layout captured by `wyrm save` rebuilds to the geometry it came from.
func TestIntegrationNestedSplitGeometry(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	r := tmux.Exec{SocketName: fmt.Sprintf("wyrm-geom-%d", os.Getpid())}
	t.Cleanup(func() { r.Run("kill-server") }) //nolint:errcheck

	root := t.TempDir()
	cfg := &config.Config{
		Session: config.Session{Name: "geom", Root: root},
		Windows: []config.Window{{
			Name: "w",
			Splits: []config.Split{
				// Left column, itself split top/bottom.
				{Command: "# left-top", Children: []config.Split{
					{Command: "# still-left-top"},
					{Type: "v", Size: 50, Command: "# left-bottom"},
				}},
				// Right column: 30% of the *window*, full height.
				{Type: "h", Size: 30, Command: "# right"},
			},
		}},
	}

	_, sessionID, _, err := session.Create(r, cfg, os.Stdout, os.Stderr)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	windows, err := tmux.ListWindows(r, sessionID)
	if err != nil || len(windows) != 1 {
		t.Fatalf("ListWindows = %v, %v; want one window", windows, err)
	}
	panes := paneGeometry(t, r, windows[0].ID)
	if len(panes) != 3 {
		t.Fatalf("got %d panes, want 3: %+v", len(panes), panes)
	}

	winW, winH := windowSize(t, r, windows[0].ID)

	// The right-hand pane must span the window's full height. Under the old
	// evaluation order it only spanned the top half of the left column's split.
	var right *paneGeom
	for i := range panes {
		if panes[i].x > 0 {
			right = &panes[i]
		}
	}
	if right == nil {
		t.Fatalf("no right-hand pane found in %+v", panes)
	}
	if right.h != winH {
		t.Errorf("right pane height = %d, want the full window height %d (it was carved out of an already-split column)", right.h, winH)
	}
	// 30% of the window width, allowing for tmux's divider and rounding.
	if want := winW * 30 / 100; abs(right.w-want) > 2 {
		t.Errorf("right pane width = %d, want ~%d (30%% of %d)", right.w, want, winW)
	}

	// And the round trip: freezing the live layout and rebuilding it must
	// produce the same geometry.
	frozen, err := freeze.Config(r, sessionID, "geom2", root)
	if err != nil {
		t.Fatalf("freeze.Config: %v", err)
	}
	frozen.Session.Name = "geom2"
	if _, secondID, _, err := session.Create(r, frozen, os.Stdout, os.Stderr); err != nil {
		t.Fatalf("Create (rebuilt): %v", err)
	} else {
		rebuiltWindows, err := tmux.ListWindows(r, secondID)
		if err != nil || len(rebuiltWindows) == 0 {
			t.Fatalf("ListWindows (rebuilt): %v, %v", rebuiltWindows, err)
		}
		rebuilt := paneGeometry(t, r, rebuiltWindows[0].ID)
		if len(rebuilt) != len(panes) {
			t.Fatalf("rebuilt has %d panes, want %d", len(rebuilt), len(panes))
		}
		for i := range panes {
			if abs(rebuilt[i].w-panes[i].w) > 2 || abs(rebuilt[i].h-panes[i].h) > 2 {
				t.Errorf("pane %d rebuilt as %dx%d at (%d,%d), want ~%dx%d at (%d,%d)",
					i, rebuilt[i].w, rebuilt[i].h, rebuilt[i].x, rebuilt[i].y,
					panes[i].w, panes[i].h, panes[i].x, panes[i].y)
			}
		}
	}
}

type paneGeom struct{ w, h, x, y int }

// paneGeometry reads every pane's size and position, ordered by tmux's own
// pane index so two windows can be compared entry by entry.
func paneGeometry(t *testing.T, r tmux.Runner, windowID string) []paneGeom {
	t.Helper()
	out, err := r.Run("list-panes", "-t", windowID, "-F", "#{pane_width}|#{pane_height}|#{pane_left}|#{pane_top}")
	if err != nil {
		t.Fatalf("list-panes: %v (%s)", err, out)
	}
	var panes []paneGeom
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "|")
		if len(f) != 4 {
			t.Fatalf("unexpected list-panes line %q", line)
		}
		panes = append(panes, paneGeom{atoi(t, f[0]), atoi(t, f[1]), atoi(t, f[2]), atoi(t, f[3])})
	}
	return panes
}

func windowSize(t *testing.T, r tmux.Runner, windowID string) (int, int) {
	t.Helper()
	out, err := r.Run("display-message", "-p", "-t", windowID, "-F", "#{window_width}|#{window_height}")
	if err != nil {
		t.Fatalf("display-message: %v (%s)", err, out)
	}
	f := strings.Split(strings.TrimSpace(out), "|")
	if len(f) != 2 {
		t.Fatalf("unexpected window size %q", out)
	}
	return atoi(t, f[0]), atoi(t, f[1])
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		t.Fatalf("not a number: %q", s)
	}
	return n
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
