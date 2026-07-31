package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jskoll/wyrm/internal/sessions"
	"github.com/jskoll/wyrm/internal/tmux"
)

// mouseModel is a sized model with a populated cascade, ready to be clicked on.
func mouseModel(t *testing.T) Model {
	t.Helper()
	m := New(nopRunner(), nil)
	m.width, m.height, m.ready = 100, 40, true
	m.projects = []Project{
		{Name: "alpha", Path: "/a/.wyrm.toml"},
		{Name: "beta", Path: "/b/.wyrm.toml", Running: true, SessionID: "$2"},
	}
	m.sessions = []sessions.Session{
		{ID: "$1", Name: "one", Windows: 2},
		{ID: "$2", Name: "two", Windows: 1},
		{ID: "$3", Name: "three", Windows: 3},
	}
	m.windows = []tmux.WindowInfo{
		{Index: 0, ID: "@1", Name: "code"},
		{Index: 1, ID: "@2", Name: "logs"},
	}
	m.panes = []tmux.PaneInfo{
		{ID: "%1", Index: 0, Command: "nvim"},
		{ID: "%2", Index: 1, Command: "zsh"},
	}
	// New starts these at -1 so the first load can snap to the active
	// window/pane; by the time anyone can click, a load has landed.
	m.windowCur, m.paneCur = 0, 0
	return m
}

func click(x, y int, button tea.MouseButton) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Button: button, Action: tea.MouseActionPress}
}

// rowY returns the screen row of entry idx in panel p, per the layout the
// renderer uses. Tests click there rather than at a hardcoded coordinate, so
// they keep testing "the row the user sees" if the layout changes.
func rowY(m Model, p panel, idx int) int {
	g := m.geometry()
	top, bottom := g.boxes[p].listRows()
	start, _ := viewport(m.cursorFor(p), m.panelLen(p), bottom-top)
	return top + (idx - start)
}

// The hit test has to agree with what was drawn: for every panel and every
// visible row, the cell the renderer put an entry on must map back to that
// entry's index.
func TestHitTestMatchesRenderedRows(t *testing.T) {
	m := mouseModel(t)
	g := m.geometry()

	for p := panel(0); p < numPanels; p++ {
		top, bottom := g.boxes[p].listRows()
		n := m.panelLen(p)
		listH := bottom - top
		start, end := viewport(m.cursorFor(p), n, listH)

		for idx := start; idx < end; idx++ {
			y := top + (idx - start)
			h, ok := m.hitTest(1, y)
			if !ok {
				t.Fatalf("panel %d row %d: hitTest(1, %d) missed", p, idx, y)
			}
			if h.panel != p || h.row != idx {
				t.Errorf("panel %d row %d at y=%d: got panel %d row %d", p, idx, y, h.panel, h.row)
			}
		}

		// The box's own borders and title are chrome, not rows.
		for _, y := range []int{g.boxes[p].y0, g.boxes[p].y0 + 1, g.boxes[p].y1 - 1} {
			h, ok := m.hitTest(1, y)
			if ok && h.row >= 0 {
				t.Errorf("panel %d: y=%d is chrome but mapped to row %d", p, y, h.row)
			}
		}
	}
}

// A panel scrolled away from the top must map clicks through its viewport
// offset, not straight from the top of the box.
func TestHitTestFollowsAScrolledPanel(t *testing.T) {
	m := mouseModel(t)
	m.sessions = nil
	for i := 0; i < 60; i++ {
		m.sessions = append(m.sessions, sessions.Session{ID: "$" + string(rune('a'+i%26)), Name: "s"})
	}
	m.sessionCur = 45
	m.focus = panelSessions

	g := m.geometry()
	top, bottom := g.boxes[panelSessions].listRows()
	start, _ := viewport(m.sessionCur, len(m.sessions), bottom-top)
	if start == 0 {
		t.Fatal("expected the sessions panel to be scrolled")
	}

	h, ok := m.hitTest(1, top)
	if !ok || h.row != start {
		t.Errorf("top visible row = %d, want %d (viewport start)", h.row, start)
	}
}

// The property that actually matters to someone using the mouse: the row you
// click is the row that lights up. It closes the loop between the hit test and
// the renderer by checking the *drawn frame*, with a real color profile so the
// selection band is actually emitted — see withColor.
func TestClickHighlightsTheRowUnderThePointer(t *testing.T) {
	withColor(t)

	base := mouseModel(t)
	selBG := bgSGR(t, DefaultTheme().Selected)

	for idx := 0; idx < len(base.sessions); idx++ {
		y := rowY(base, panelSessions, idx)
		next, _ := base.Update(click(2, y, tea.MouseButtonLeft))
		lines := strings.Split(next.(Model).View(), "\n")

		if !strings.Contains(lines[y], selBG) {
			t.Errorf("clicked row %d at y=%d: that line is not highlighted", idx, y)
		}
		// and nothing else in the panel is.
		for other := 0; other < len(base.sessions); other++ {
			if other == idx {
				continue
			}
			oy := rowY(base, panelSessions, other)
			if strings.Contains(lines[oy], selBG) {
				t.Errorf("clicking row %d also highlighted row %d", idx, other)
			}
		}
	}
}

func TestHitTestRejectsPreviewAndFooter(t *testing.T) {
	m := mouseModel(t)
	g := m.geometry()

	if _, ok := m.hitTest(g.leftW+5, 3); ok {
		t.Error("a click in the preview pane should not hit a panel")
	}
	if _, ok := m.hitTest(1, m.height-1); ok {
		t.Error("a click on the footer should not hit a panel")
	}
}

func TestClickFocusesPanelAndSelectsRow(t *testing.T) {
	m := mouseModel(t)
	m.focus = panelSessions

	y := rowY(m, panelWindows, 1)
	next, _ := m.Update(click(2, y, tea.MouseButtonLeft))
	m = next.(Model)

	if m.focus != panelWindows {
		t.Errorf("focus = %d, want panelWindows", m.focus)
	}
	if m.windowCur != 1 {
		t.Errorf("windowCur = %d, want 1", m.windowCur)
	}
}

// Clicking a panel's border or title focuses it without moving its selection —
// otherwise a click aimed at the frame would silently re-target the cascade.
func TestClickOnChromeFocusesWithoutSelecting(t *testing.T) {
	m := mouseModel(t)
	m.focus = panelSessions
	m.paneCur = 1

	g := m.geometry()
	next, _ := m.Update(click(2, g.boxes[panelPanes].y0+1, tea.MouseButtonLeft)) // title row
	m = next.(Model)

	if m.focus != panelPanes {
		t.Errorf("focus = %d, want panelPanes", m.focus)
	}
	if m.paneCur != 1 {
		t.Errorf("paneCur = %d, want it left at 1", m.paneCur)
	}
}

func TestDoubleClickAttaches(t *testing.T) {
	m := mouseModel(t)
	now := time.Now()
	m.clock = func() time.Time { return now }
	m.focus = panelSessions

	y := rowY(m, panelSessions, 2)
	next, _ := m.Update(click(2, y, tea.MouseButtonLeft))
	m = next.(Model)
	if m.pendingAttach != "" {
		t.Fatal("a single click must not attach")
	}

	now = now.Add(100 * time.Millisecond)
	next, _ = m.Update(click(2, y, tea.MouseButtonLeft))
	m = next.(Model)
	if m.pendingAttach != "$3" {
		t.Errorf("pendingAttach = %q, want %q", m.pendingAttach, "$3")
	}
}

func TestTwoSlowClicksAreNotADoubleClick(t *testing.T) {
	m := mouseModel(t)
	now := time.Now()
	m.clock = func() time.Time { return now }

	y := rowY(m, panelSessions, 1)
	next, _ := m.Update(click(2, y, tea.MouseButtonLeft))
	m = next.(Model)

	now = now.Add(doubleClickWindow + time.Millisecond)
	next, _ = m.Update(click(2, y, tea.MouseButtonLeft))
	m = next.(Model)

	if m.pendingAttach != "" {
		t.Error("clicks outside the double-click window must not attach")
	}
}

// Two clicks in quick succession on *different* rows are two single clicks.
func TestDoubleClickRequiresTheSameRow(t *testing.T) {
	m := mouseModel(t)
	now := time.Now()
	m.clock = func() time.Time { return now }

	next, _ := m.Update(click(2, rowY(m, panelSessions, 0), tea.MouseButtonLeft))
	m = next.(Model)
	next, _ = m.Update(click(2, rowY(m, panelSessions, 1), tea.MouseButtonLeft))
	m = next.(Model)

	if m.pendingAttach != "" {
		t.Error("clicks on different rows must not attach")
	}
}

func TestWheelMovesTheHoveredPanel(t *testing.T) {
	m := mouseModel(t)
	m.focus = panelSessions
	m.sessionCur = 0

	y := rowY(m, panelSessions, 1)
	next, _ := m.Update(tea.MouseMsg{X: 2, Y: y, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	m = next.(Model)
	if m.sessionCur != 1 {
		t.Errorf("sessionCur = %d, want 1 after wheel down", m.sessionCur)
	}

	next, _ = m.Update(tea.MouseMsg{X: 2, Y: y, Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	m = next.(Model)
	if m.sessionCur != 0 {
		t.Errorf("sessionCur = %d, want 0 after wheel up", m.sessionCur)
	}
}

// The wheel takes focus with it, because which list a cursor indexes depends on
// which panel is focused (only the focused panel is filtered).
func TestWheelTakesFocus(t *testing.T) {
	m := mouseModel(t)
	m.focus = panelSessions

	y := rowY(m, panelPanes, 0)
	next, _ := m.Update(tea.MouseMsg{X: 2, Y: y, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	m = next.(Model)

	if m.focus != panelPanes {
		t.Errorf("focus = %d, want panelPanes", m.focus)
	}
}

func TestMouseIgnoredWhenDisabled(t *testing.T) {
	m := mouseModel(t)
	m.mouseOn = false
	m.focus = panelSessions

	next, _ := m.Update(click(2, rowY(m, panelWindows, 1), tea.MouseButtonLeft))
	m = next.(Model)

	if m.focus != panelSessions {
		t.Error("mouse events must be ignored while capture is off")
	}
}

// A click behind an open confirm prompt must not re-target the pending action.
func TestMouseIgnoredBehindAModal(t *testing.T) {
	m := mouseModel(t)
	m.focus = panelSessions
	m.sessionCur = 0
	next, _ := m.Update(key("x")) // opens the kill confirm
	m = next.(Model)
	if m.mode != modeConfirm {
		t.Fatalf("mode = %d, want modeConfirm", m.mode)
	}

	next, _ = m.Update(click(2, rowY(m, panelSessions, 2), tea.MouseButtonLeft))
	m = next.(Model)

	if m.sessionCur != 0 {
		t.Errorf("sessionCur = %d, want it unchanged behind the modal", m.sessionCur)
	}
	if m.pending.sessionID != "$1" {
		t.Errorf("pending target = %q, want it unchanged", m.pending.sessionID)
	}
}

func TestMouseToggleKey(t *testing.T) {
	m := New(nopRunner(), nil)
	if !m.mouseOn {
		t.Fatal("mouse should default to on")
	}
	next, cmd := m.Update(key("m"))
	m = next.(Model)
	if m.mouseOn {
		t.Error("'m' should turn mouse capture off")
	}
	if cmd == nil {
		t.Error("toggling should emit the terminal mouse-mode command")
	}
	next, _ = m.Update(key("m"))
	if !next.(Model).mouseOn {
		t.Error("'m' should turn mouse capture back on")
	}
}

// A click must clear a displayed error, the same way any keypress does.
func TestClickClearsError(t *testing.T) {
	m := mouseModel(t)
	m.err = errFake{}
	next, _ := m.Update(click(2, rowY(m, panelSessions, 1), tea.MouseButtonLeft))
	if next.(Model).err != nil {
		t.Error("a click should dismiss the error footer")
	}
}

type errFake struct{}

func (errFake) Error() string { return "boom" }

// Double-clicking in the Projects panel starts that project rather than
// attaching to whatever session is selected — the same split Enter makes.
func TestDoubleClickOnProjectStartsIt(t *testing.T) {
	m := mouseModel(t)
	now := time.Now()
	m.clock = func() time.Time { return now }

	y := rowY(m, panelProjects, 0)
	next, _ := m.Update(click(2, y, tea.MouseButtonLeft))
	m = next.(Model)
	now = now.Add(50 * time.Millisecond)
	next, cmd := m.Update(click(2, y, tea.MouseButtonLeft))
	m = next.(Model)

	if m.focus != panelProjects {
		t.Fatalf("focus = %d, want panelProjects", m.focus)
	}
	if m.pendingAttach != "" {
		t.Error("a project double-click must not attach directly; it starts the project")
	}
	// startProject resolves through a command that reports back with
	// projectStartedMsg (here: an error, since the config path is fake).
	if _, ok := run(cmd).(projectStartedMsg); !ok {
		t.Errorf("expected a projectStartedMsg, got %T", run(cmd))
	}
}
