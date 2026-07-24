package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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
	m.width, m.height, m.ready = 120, 40, true

	// "?" opens the overlay.
	m, _ = update(m, key("?"))
	if m.mode != modeHelp {
		t.Fatalf("mode = %d, want modeHelp after '?'", m.mode)
	}
	out := m.View()
	for _, want := range []string{"keyboard shortcuts", "cycle the window layout", "toggle zoom", "runs on_project_exit"} {
		if !strings.Contains(out, want) {
			t.Errorf("help overlay missing %q\n%s", want, out)
		}
	}

	// esc dismisses it.
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != modeNormal {
		t.Errorf("mode = %d, want modeNormal after esc", m.mode)
	}
	// q also dismisses.
	m, _ = update(m, key("?"))
	m, _ = update(m, key("q"))
	if m.mode != modeNormal {
		t.Errorf("q should close the help overlay")
	}
}

func TestHelpOverlayScrolls(t *testing.T) {
	// A short terminal can't show every binding at once; the overlay must
	// scroll rather than clip off-screen.
	m := New(nopRunner(), nil)
	m.width, m.height, m.ready = 60, 12, true // narrow -> single column, short -> must scroll
	m, _ = update(m, key("?"))

	if m.helpMaxScroll() <= 0 {
		t.Fatalf("expected the help to overflow a 12-row terminal (maxScroll=%d)", m.helpMaxScroll())
	}

	// The first binding is visible at the top, the last is not.
	top := m.View()
	if !strings.Contains(top, "cycle focus between panels") {
		t.Errorf("top of help should show the first Global binding\n%s", top)
	}
	if strings.Contains(top, "cancel") {
		t.Errorf("bottom binding 'cancel' should be scrolled out of view initially\n%s", top)
	}

	// Jump to the bottom: the last binding becomes visible.
	m, _ = update(m, key("G"))
	if m.helpScroll != m.helpMaxScroll() {
		t.Errorf("G should jump to the bottom: scroll=%d max=%d", m.helpScroll, m.helpMaxScroll())
	}
	bottom := m.View()
	if !strings.Contains(bottom, "cancel") {
		t.Errorf("bottom of help should reveal the last binding\n%s", bottom)
	}

	// Scrolling never runs past the ends.
	m, _ = update(m, key("j"))
	if m.helpScroll != m.helpMaxScroll() {
		t.Errorf("scrolling past the bottom should clamp: scroll=%d max=%d", m.helpScroll, m.helpMaxScroll())
	}
	m, _ = update(m, key("g"))
	if m.helpScroll != 0 {
		t.Errorf("g should jump to the top, got scroll=%d", m.helpScroll)
	}
}

func TestViewEmptySessions(t *testing.T) {
	m := New(nopRunner(), nil)
	m.width, m.height, m.ready = 100, 30, true
	if out := m.View(); !strings.Contains(out, "no running sessions") {
		t.Errorf("View() with no sessions should show the empty hint\n%s", out)
	}
}
