package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jskoll/wyrm/internal/tmux"
)

// findPaneModel is a sized, ready model with a small whole-server pane list,
// as if a loadAllPanes had already landed.
func findPaneModel(t *testing.T) Model {
	t.Helper()
	m := New(nopRunner(), nil)
	m.width, m.height, m.ready = 100, 40, true
	m.allPanes = []tmux.PaneRef{
		{SessionID: "$1", SessionName: "webapp", WindowID: "@1", WindowIndex: 0, WindowName: "code", PaneID: "%1", PaneIndex: 0, Command: "nvim"},
		{SessionID: "$1", SessionName: "webapp", WindowID: "@2", WindowIndex: 1, WindowName: "server", PaneID: "%2", PaneIndex: 0, Command: "npm"},
		{SessionID: "$2", SessionName: "dotfiles", WindowID: "@3", WindowIndex: 0, WindowName: "main", PaneID: "%3", PaneIndex: 0, Command: "zsh"},
	}
	return m
}

// TestFindPaneKeyOpensOverlay covers the trigger: "f" from normal mode
// enters modeFindPane and kicks off a load of the whole-server list.
func TestFindPaneKeyOpensOverlay(t *testing.T) {
	m := mouseModel(t)
	next, cmd := update(m, key("f"))
	if next.mode != modeFindPane {
		t.Errorf("mode = %v, want modeFindPane", next.mode)
	}
	if cmd == nil {
		t.Error("\"f\" produced no command; want it to load the whole-server pane list")
	}
}

// TestFindPaneKeyDisabledInCompact: wyrm pick's two-panel form doesn't get
// the flat search — it's deliberately scoped to the full TUI.
func TestFindPaneKeyDisabledInCompact(t *testing.T) {
	m := mouseModel(t)
	m.compact = true
	next, cmd := update(m, key("f"))
	if next.mode == modeFindPane {
		t.Error("\"f\" opened modeFindPane in compact mode, want it disabled")
	}
	if cmd != nil {
		t.Error("\"f\" in compact mode produced a command, want none")
	}
}

// TestFindPaneTypingNarrowsList covers the query filtering every field a
// flat list needs to disambiguate: session name, window name, and command.
func TestFindPaneTypingNarrowsList(t *testing.T) {
	m := findPaneModel(t)
	m.mode = modeFindPane

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("dotfiles")})
	list := m.visibleAllPanes()
	if len(list) != 1 || list[0].PaneID != "%3" {
		t.Errorf("visibleAllPanes() = %+v, want just the dotfiles pane", list)
	}
}

func TestFindPaneBackspaceWidensQuery(t *testing.T) {
	m := findPaneModel(t)
	m.mode = modeFindPane
	m.findPaneQuery = "dotfiles"

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.findPaneQuery != "dotfile" {
		t.Errorf("findPaneQuery = %q, want %q", m.findPaneQuery, "dotfile")
	}
}

// TestFindPaneEnterAttachesToSelection covers Enter resolving straight to
// the selected row's session/window/pane and quitting — the same
// selectTargetCmd sequence the Panes panel's Enter uses, just addressed
// directly instead of via the selected-session/selected-window chain.
func TestFindPaneEnterAttachesToSelection(t *testing.T) {
	m := findPaneModel(t)
	m.mode = modeFindPane
	m.findPaneCur = 1 // the "server" pane, %2

	var calls []string
	r := funcRunner{fn: func(args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		return "", nil
	}}
	m.runner = r

	next, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if next.pendingAttach != "$1" {
		t.Errorf("pendingAttach = %q, want the session ID %q", next.pendingAttach, "$1")
	}
	if next.mode != modeNormal {
		t.Errorf("mode = %v, want modeNormal after attaching", next.mode)
	}
	if cmd == nil {
		t.Fatal("Enter produced no command; want select-window/select-pane + quit")
	}
	// Enter returns a tea.Sequence whose inner closures aren't run by
	// invoking the outer cmd (see TestAttachPreSelectsWindowAndPane), so
	// exercise the pre-select command directly.
	run(selectTargetCmd(r, "@2", "%2"))
	joined := strings.Join(calls, " | ")
	if !strings.Contains(joined, "@2") {
		t.Errorf("calls = %q, want the target window @2 selected", joined)
	}
	if !strings.Contains(joined, "%2") {
		t.Errorf("calls = %q, want the target pane %%2 selected", joined)
	}
}

// TestFindPaneEnterOnEmptyListDoesNothing guards against attaching to a
// zero-value PaneRef when the filtered list is empty.
func TestFindPaneEnterOnEmptyListDoesNothing(t *testing.T) {
	m := New(nopRunner(), nil)
	m.width, m.height, m.ready = 100, 40, true
	m.mode = modeFindPane
	m.findPaneQuery = "nothing matches this"

	next, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if next.pendingAttach != "" {
		t.Errorf("pendingAttach = %q, want empty", next.pendingAttach)
	}
	if cmd != nil {
		t.Error("Enter on an empty list produced a command, want none")
	}
}

// TestFindPaneEscClosesAndClearsQuery.
func TestFindPaneEscClosesAndClearsQuery(t *testing.T) {
	m := findPaneModel(t)
	m.mode = modeFindPane
	m.findPaneQuery = "dotfiles"

	next, _ := update(m, tea.KeyMsg{Type: tea.KeyEsc})
	if next.mode != modeNormal {
		t.Errorf("mode = %v, want modeNormal", next.mode)
	}
	if next.findPaneQuery != "" {
		t.Errorf("findPaneQuery = %q, want cleared", next.findPaneQuery)
	}
}

// TestFindPaneArrowsMoveSelection.
func TestFindPaneArrowsMoveSelection(t *testing.T) {
	m := findPaneModel(t)
	m.mode = modeFindPane

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.findPaneCur != 1 {
		t.Errorf("findPaneCur after down = %d, want 1", m.findPaneCur)
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.findPaneCur != 0 {
		t.Errorf("findPaneCur after up = %d, want 0", m.findPaneCur)
	}
}

// TestAllPanesMsgClampsSelection: a shorter list landing after the cursor
// was pushed deep into a longer one must not leave the cursor out of range.
func TestAllPanesMsgClampsSelection(t *testing.T) {
	m := findPaneModel(t)
	m.mode = modeFindPane
	m.findPaneCur = 2

	m, _ = update(m, allPanesMsg{panes: m.allPanes[:1]})
	if m.findPaneCur != 0 {
		t.Errorf("findPaneCur = %d, want clamped to 0", m.findPaneCur)
	}
}

// TestFindPaneOverlayRenders is a smoke test that the overlay doesn't panic
// and shows the query and at least one row.
func TestFindPaneOverlayRenders(t *testing.T) {
	m := findPaneModel(t)
	m.mode = modeFindPane
	m.findPaneQuery = "web"

	out := m.View()
	if !strings.Contains(out, "find pane") {
		t.Errorf("View() = %q, want the overlay title", out)
	}
	if !strings.Contains(out, "web") {
		t.Errorf("View() = %q, want the typed query", out)
	}
	if !strings.Contains(out, "nvim") {
		t.Errorf("View() = %q, want a matching row", out)
	}
}
