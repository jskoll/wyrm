package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func rightClick(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonRight, Action: tea.MouseActionPress}
}

func TestRightClickOpensMenuOnTheClickedRow(t *testing.T) {
	m := mouseModel(t)
	m.focus = panelSessions

	y := rowY(m, panelWindows, 1)
	next, _ := m.Update(rightClick(3, y))
	m = next.(Model)

	if m.mode != modeMenu {
		t.Fatalf("mode = %d, want modeMenu", m.mode)
	}
	if m.focus != panelWindows {
		t.Errorf("focus = %d, want panelWindows", m.focus)
	}
	// The menu must act on the row under the pointer, not on the previous
	// selection — this is the whole point of selecting before opening.
	if m.windowCur != 1 {
		t.Errorf("windowCur = %d, want 1", m.windowCur)
	}
	if len(m.menu) == 0 {
		t.Error("expected menu entries for the Windows panel")
	}
}

func TestMenuEntriesPerPanel(t *testing.T) {
	m := mouseModel(t)
	tests := []struct {
		panel panel
		want  []menuOp
	}{
		{panelSessions, []menuOp{menuAttach, menuRename, menuNewWindow, menuKill}},
		{panelWindows, []menuOp{menuAttach, menuRename, menuNewWindow, menuLayout, menuKill}},
		{panelPanes, []menuOp{menuAttach, menuZoom, menuKill}},
	}
	for _, tt := range tests {
		got := m.menuFor(tt.panel)
		if len(got) != len(tt.want) {
			t.Errorf("panel %d: %d entries, want %d", tt.panel, len(got), len(tt.want))
			continue
		}
		for i, op := range tt.want {
			if got[i].op != op {
				t.Errorf("panel %d entry %d: op %d, want %d", tt.panel, i, got[i].op, op)
			}
		}
	}
}

// "Stop project" would fail on a project that isn't running, so it's only
// offered when it applies.
func TestProjectMenuOffersStopOnlyWhenRunning(t *testing.T) {
	m := mouseModel(t)

	m.projectCur = 0 // not running
	for _, e := range m.menuFor(panelProjects) {
		if e.op == menuKill {
			t.Error("a stopped project should not offer Stop")
		}
	}

	m.projectCur = 1 // running
	found := false
	for _, e := range m.menuFor(panelProjects) {
		if e.op == menuKill {
			found = true
		}
	}
	if !found {
		t.Error("a running project should offer Stop")
	}
}

func TestMenuKeyboardNavigationWraps(t *testing.T) {
	m := mouseModel(t)
	next, _ := m.Update(rightClick(3, rowY(m, panelPanes, 0)))
	m = next.(Model)
	n := len(m.menu)

	next, _ = m.Update(key("k")) // up from the first entry wraps to the last
	m = next.(Model)
	if m.menuCur != n-1 {
		t.Errorf("menuCur = %d, want %d", m.menuCur, n-1)
	}
	next, _ = m.Update(key("j")) // and back around
	m = next.(Model)
	if m.menuCur != 0 {
		t.Errorf("menuCur = %d, want 0", m.menuCur)
	}
}

func TestMenuEscCloses(t *testing.T) {
	m := mouseModel(t)
	next, _ := m.Update(rightClick(3, rowY(m, panelSessions, 0)))
	m = next.(Model)

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.mode != modeNormal {
		t.Errorf("mode = %d, want modeNormal", m.mode)
	}
	if m.menu != nil {
		t.Error("closing the menu should drop its entries")
	}
}

// Picking Kill from the menu hands off to the same confirm modal the "x" key
// opens, rather than killing outright.
func TestMenuKillOpensTheConfirmModal(t *testing.T) {
	m := mouseModel(t)
	m.sessionCur = 1
	next, _ := m.Update(rightClick(3, rowY(m, panelSessions, 1)))
	m = next.(Model)

	for i, e := range m.menu {
		if e.op == menuKill {
			m.menuCur = i
		}
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if m.mode != modeConfirm {
		t.Fatalf("mode = %d, want modeConfirm", m.mode)
	}
	if m.pending.op != opKillSession || m.pending.sessionID != "$2" {
		t.Errorf("pending = %+v, want a kill of $2", m.pending)
	}
}

func TestMenuRenameOpensThePrompt(t *testing.T) {
	m := mouseModel(t)
	next, _ := m.Update(rightClick(3, rowY(m, panelSessions, 0)))
	m = next.(Model)
	for i, e := range m.menu {
		if e.op == menuRename {
			m.menuCur = i
		}
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if m.mode != modePrompt {
		t.Fatalf("mode = %d, want modePrompt", m.mode)
	}
	if m.textInput.Value() != "one" {
		t.Errorf("prompt seeded with %q, want %q", m.textInput.Value(), "one")
	}
}

func TestClickOutsideMenuDismissesIt(t *testing.T) {
	m := mouseModel(t)
	next, _ := m.Update(rightClick(3, rowY(m, panelSessions, 0)))
	m = next.(Model)

	x, y, w, _ := m.menuBox()
	next, _ = m.Update(click(x+w+3, y, tea.MouseButtonLeft))
	m = next.(Model)

	if m.mode != modeNormal {
		t.Errorf("mode = %d, want modeNormal after clicking away", m.mode)
	}
}

func TestClickOnMenuEntryRunsIt(t *testing.T) {
	m := mouseModel(t)
	m.sessionCur = 0
	next, _ := m.Update(rightClick(3, rowY(m, panelSessions, 0)))
	m = next.(Model)

	killIdx := -1
	for i, e := range m.menu {
		if e.op == menuKill {
			killIdx = i
		}
	}
	x, y, _, _ := m.menuBox()
	next, _ = m.Update(click(x+1, y+1+killIdx, tea.MouseButtonLeft))
	m = next.(Model)

	if m.mode != modeConfirm {
		t.Errorf("mode = %d, want modeConfirm from clicking Kill", m.mode)
	}
}

// The menu is clamped so it never hangs off the screen, wherever it's opened.
func TestMenuBoxStaysOnScreen(t *testing.T) {
	m := mouseModel(t)
	for _, pt := range [][2]int{{0, 0}, {m.width - 1, m.height - 1}, {m.width - 1, 0}, {0, m.height - 1}} {
		next, _ := m.Update(rightClick(pt[0], pt[1]))
		mm := next.(Model)
		if mm.mode != modeMenu {
			continue // no entries at that spot; nothing to clamp
		}
		x, y, w, h := mm.menuBox()
		if x < 0 || y < 0 || x+w > mm.width || y+h > mm.height {
			t.Errorf("menu at click %v -> box (%d,%d,%d,%d) escapes %dx%d",
				pt, x, y, w, h, mm.width, mm.height)
		}
	}
}

// Compositing the menu must not change any line's display width: a frame whose
// rows differ in width tears the layout. Rendered with a real color profile,
// because the escape sequences are exactly what the splice has to handle — see
// withColor.
func TestOverlayPreservesLineWidths(t *testing.T) {
	withColor(t)

	m := mouseModel(t)
	before := strings.Split(m.View(), "\n")

	next, _ := m.Update(rightClick(3, rowY(m, panelSessions, 1)))
	m = next.(Model)
	after := strings.Split(m.View(), "\n")

	if len(before) != len(after) {
		t.Fatalf("frame changed height: %d -> %d lines", len(before), len(after))
	}
	for i := range before {
		bw, aw := ansi.StringWidth(before[i]), ansi.StringWidth(after[i])
		if bw != aw {
			t.Errorf("line %d: width %d -> %d with the menu open", i, bw, aw)
		}
	}
}

func TestOverlayDrawsTheMenuInTheFrame(t *testing.T) {
	m := mouseModel(t)
	next, _ := m.Update(rightClick(3, rowY(m, panelSessions, 1)))
	m = next.(Model)

	out := m.View()
	for _, want := range []string{"Rename session", "Kill session", "New window"} {
		if !strings.Contains(out, want) {
			t.Errorf("frame missing menu entry %q", want)
		}
	}
}

// overlay works in terminal cells: a base line full of escape sequences must be
// cut on cell boundaries, never mid-sequence.
func TestOverlayCutsOnCellBoundaries(t *testing.T) {
	withColor(t)

	base := strings.Repeat("\x1b[31mabc\x1b[0m", 10) // 30 cells of colored text
	out := overlay(base, "XX", 5, 0)

	if got := ansi.StringWidth(out); got != 30 {
		t.Errorf("width = %d, want 30", got)
	}
	plain := ansi.Strip(out)
	if plain[5:7] != "XX" {
		t.Errorf("box landed at %q, want columns 5-6", plain[5:7])
	}
	if plain[:5] != "abcab" {
		t.Errorf("left of the box = %q, want %q", plain[:5], "abcab")
	}
}

func TestOverlayIgnoresOutOfRangeRows(t *testing.T) {
	base := "one\ntwo"
	if got := overlay(base, "X", 0, 99); got != base {
		t.Errorf("overlay below the frame changed it: %q", got)
	}
	if got := overlay(base, "X", 0, -1); got != base {
		t.Errorf("overlay above the frame changed it: %q", got)
	}
}
