package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// doubleClickWindow is how close together two clicks on the same row must fall
// to count as a double click. 500ms is the usual desktop default and, more to
// the point, comfortably longer than the TUI's own refresh tick — a double
// click that straddled a reload would otherwise be scored as two single ones.
const doubleClickWindow = 500 * time.Millisecond

// handleMouse routes a mouse event. Mouse reporting is only requested when the
// setting is on, but the terminal can deliver a stray event either side of a
// toggle, so the disabled case is handled rather than assumed away.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if !m.mouseOn {
		return m, nil
	}
	if m.mode == modeMenu {
		return m.handleMenuMouse(msg)
	}
	// Modals own the screen: a click behind a confirm prompt must not quietly
	// re-target the action being confirmed. Filtering is not modal in that
	// sense — the list under it is live — so the mouse keeps working there.
	if m.mode != modeNormal && m.mode != modeFilter {
		return m, nil
	}

	switch msg.Button {
	case tea.MouseButtonWheelUp:
		return m.wheel(msg, -1)
	case tea.MouseButtonWheelDown:
		return m.wheel(msg, 1)
	case tea.MouseButtonLeft:
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		return m.leftClick(msg)
	case tea.MouseButtonRight:
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		return m.rightClick(msg)
	}
	return m, nil
}

// wheel scrolls the panel under the pointer, whether or not it has focus, and
// takes focus with it.
//
// Focus follows the wheel on purpose. Which list a cursor indexes depends on
// focus — only the focused panel is filtered (see filterFor) — so moving a
// selection in a panel that doesn't have focus would have it index a different
// list than the one the user is looking at.
func (m Model) wheel(msg tea.MouseMsg, delta int) (tea.Model, tea.Cmd) {
	h, ok := m.hitTest(msg.X, msg.Y)
	if !ok {
		return m, nil
	}
	var cmd tea.Cmd
	if m.focus != h.panel {
		m.focus = h.panel
		cmd = m.updatePreview()
	}
	next, moveCmd := m.setCursor(h.panel, m.cursorFor(h.panel)+delta)
	return next, tea.Batch(cmd, moveCmd)
}

// leftClick focuses the clicked panel and selects the clicked row. A second
// click on the same row within doubleClickWindow activates it — attaching to
// the session, or starting the project — which is Enter's job from the
// keyboard.
func (m Model) leftClick(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	h, ok := m.hitTest(msg.X, msg.Y)
	if !ok {
		return m, nil
	}
	// Any click clears a reported error, matching the keyboard's behavior of
	// returning the footer to its hints on the next input.
	m.err = nil

	var cmds []tea.Cmd
	if m.focus != h.panel {
		m.focus = h.panel
		cmds = append(cmds, m.updatePreview())
	}
	if h.row < 0 {
		// The panel's chrome: focus it, but leave the selection alone.
		m.clickCount = 0
		return m, tea.Batch(cmds...)
	}

	next, cmd := m.setCursor(h.panel, h.row)
	m = next.(Model)
	cmds = append(cmds, cmd)

	if m.isDoubleClick(msg, h) {
		m.clickCount = 0
		activated, cmd := m.activateSelection()
		return activated, tea.Batch(append(cmds, cmd)...)
	}
	m.clickCount = 1
	m.lastClickAt = m.now()
	m.lastClickPanel, m.lastClickRow = h.panel, h.row
	return m, tea.Batch(cmds...)
}

// isDoubleClick reports whether this press completes a double click: a previous
// press on the same row of the same panel, recently enough.
func (m Model) isDoubleClick(_ tea.MouseMsg, h hit) bool {
	if m.clickCount == 0 {
		return false
	}
	if m.lastClickPanel != h.panel || m.lastClickRow != h.row {
		return false
	}
	return m.now().Sub(m.lastClickAt) <= doubleClickWindow
}

// activateSelection is the click equivalent of Enter.
func (m Model) activateSelection() (tea.Model, tea.Cmd) {
	if m.focus == panelProjects {
		return m.startProject()
	}
	return m.attachToSelection()
}

// rightClick selects what was clicked and opens the context menu for it, so the
// action the user picks always applies to the row under the pointer rather than
// to whatever happened to be selected beforehand.
func (m Model) rightClick(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	h, ok := m.hitTest(msg.X, msg.Y)
	if !ok {
		return m, nil
	}
	m.err = nil
	m.clickCount = 0

	var cmds []tea.Cmd
	if m.focus != h.panel {
		m.focus = h.panel
		cmds = append(cmds, m.updatePreview())
	}
	if h.row >= 0 {
		next, cmd := m.setCursor(h.panel, h.row)
		m = next.(Model)
		cmds = append(cmds, cmd)
	}

	opened, ok := m.openMenu(h.panel, msg.X, msg.Y)
	if !ok {
		return m, tea.Batch(cmds...)
	}
	return opened, tea.Batch(cmds...)
}

// now is the model's clock. Tests substitute it to drive the double-click
// window deterministically instead of sleeping.
func (m Model) now() time.Time {
	if m.clock != nil {
		return m.clock()
	}
	return time.Now()
}

// mouseCmd returns the Bubble Tea command that switches terminal mouse
// reporting to match m.mouseOn.
func (m Model) mouseCmd() tea.Cmd {
	if m.mouseOn {
		return tea.EnableMouseCellMotion
	}
	return tea.DisableMouse
}
