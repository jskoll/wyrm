package tui

import (
	"strings"
	"testing"

	"github.com/jskoll/wyrm/internal/picker"
	"github.com/jskoll/wyrm/internal/tmux"
)

func TestViewTooSmall(t *testing.T) {
	m := New(nopRunner(), nil)
	m.width, m.height, m.ready = 10, 4, true
	if got := m.View(); !strings.Contains(got, "too small") {
		t.Errorf("View() at 10x4 = %q, want a too-small notice", got)
	}
}

func TestViewRendersPanels(t *testing.T) {
	m := New(nopRunner(), nil)
	m.width, m.height, m.ready = 100, 30, true
	m.sessions = []picker.Session{{ID: "$1", Name: "webapp", Windows: 2, Attached: true}}
	m.windows = []tmux.WindowInfo{{Index: 0, ID: "@1", Name: "code", Active: true}}
	m.panes = []tmux.PaneInfo{{ID: "%1", Index: 0, Command: "nvim", Active: true}}
	m.preview = "some pane output"
	m.previewTitle = "%1 nvim"

	out := m.View()
	for _, want := range []string{"Sessions", "Windows", "Panes", "webapp", "code", "nvim", "some pane output"} {
		if !strings.Contains(out, want) {
			t.Errorf("View() missing %q\n---\n%s", want, out)
		}
	}
	// The frame must not exceed the terminal height.
	if lines := strings.Count(out, "\n") + 1; lines > m.height {
		t.Errorf("View() rendered %d lines, exceeds height %d", lines, m.height)
	}
}

func TestHelpOverlay(t *testing.T) {
	m := New(nopRunner(), nil)
	m.width, m.height, m.ready = 100, 40, true

	// "?" opens the overlay.
	m, _ = update(m, key("?"))
	if m.mode != modeHelp {
		t.Fatalf("mode = %d, want modeHelp after '?'", m.mode)
	}
	out := m.View()
	for _, want := range []string{"keyboard shortcuts", "cycle the window layout", "toggle zoom", "runs on_project_exit", "press any key to close"} {
		if !strings.Contains(out, want) {
			t.Errorf("help overlay missing %q\n%s", want, out)
		}
	}

	// Any key dismisses it.
	m, _ = update(m, key("j"))
	if m.mode != modeNormal {
		t.Errorf("mode = %d, want modeNormal after dismiss key", m.mode)
	}
}

func TestViewEmptySessions(t *testing.T) {
	m := New(nopRunner(), nil)
	m.width, m.height, m.ready = 100, 30, true
	if out := m.View(); !strings.Contains(out, "no running sessions") {
		t.Errorf("View() with no sessions should show the empty hint\n%s", out)
	}
}
