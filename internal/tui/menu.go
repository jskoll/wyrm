package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// menuOp is an entry in the right-click context menu. Like pendingAction's op,
// it's an enum rather than a closure so the Model stays plain data.
type menuOp int

const (
	menuAttach menuOp = iota
	menuStart
	menuEdit
	menuRename
	menuNewWindow
	menuLayout
	menuZoom
	menuKill
	menuSwapUp
	menuSwapDown
	menuMoveWindow
	menuSplitV
	menuSplitH
)

// menuEntry is one row of the menu. key is the keyboard equivalent, shown
// alongside so the menu teaches the bindings rather than replacing them.
type menuEntry struct {
	op    menuOp
	label string
	key   string
}

// menuFor returns the actions available for the current selection in p, or nil
// when there's nothing to act on. The entries mirror the panel's footer hints:
// a menu offering an action the panel doesn't otherwise have would be a second,
// divergent set of bindings. Both come from the panel table — see panels.go.
func (m Model) menuFor(p panel) []menuEntry {
	if f := p.spec().menu; f != nil {
		return f(m)
	}
	return nil
}

func projectMenu(m Model) []menuEntry {
	proj, ok := m.currentProject()
	if !ok {
		return nil
	}
	entries := []menuEntry{
		{menuStart, "Start / attach", "↵"},
		{menuEdit, "Edit config", "e"},
	}
	// Stopping a project that isn't running is the one action here that would
	// fail rather than no-op, so it's offered only when it applies.
	if proj.Running {
		entries = append(entries, menuEntry{menuKill, "Stop project", "x"})
	}
	return entries
}

func sessionMenu(m Model) []menuEntry {
	e, ok := m.currentSessionEntry()
	if !ok {
		return nil
	}
	// A stopped row can only be started — everything below targets a session
	// ID it doesn't have.
	if !e.Running {
		if !e.HasProject {
			return nil
		}
		return []menuEntry{{menuStart, "Start session", "↵"}}
	}
	return []menuEntry{
		{menuAttach, "Attach", "↵"},
		{menuRename, "Rename session", "r"},
		{menuNewWindow, "New window", "n"},
		{menuKill, "Kill session", "x"},
	}
}

func windowMenu(m Model) []menuEntry {
	if _, ok := m.currentWindow(); !ok {
		return nil
	}
	return []menuEntry{
		{menuAttach, "Attach here", "↵"},
		{menuRename, "Rename window", "r"},
		{menuNewWindow, "New window", "n"},
		{menuSwapUp, "Move up", "<"},
		{menuSwapDown, "Move down", ">"},
		{menuMoveWindow, "Move to session...", "W"},
		{menuSplitV, "Split vertically", "s"},
		{menuSplitH, "Split horizontally", "S"},
		{menuLayout, "Cycle layout", "L"},
		{menuKill, "Kill window", "x"},
	}
}

func paneMenu(m Model) []menuEntry {
	if _, ok := m.currentPane(); !ok {
		return nil
	}
	return []menuEntry{
		{menuAttach, "Attach here", "↵"},
		{menuSplitV, "Split vertically", "s"},
		{menuSplitH, "Split horizontally", "S"},
		{menuZoom, "Toggle zoom", "z"},
		{menuKill, "Kill pane", "x"},
	}
}

// openMenu builds the menu for panel p anchored at the click point. It reports
// false when the panel has no actions to offer, leaving the mode alone.
func (m Model) openMenu(p panel, x, y int) (Model, bool) {
	entries := m.menuFor(p)
	if len(entries) == 0 {
		return m, false
	}
	m.menu = entries
	m.menuCur = 0
	m.menuX, m.menuY = x, y
	m.mode = modeMenu
	return m, true
}

// openMenuAtSelection opens the context menu on the focused panel's current
// row, anchored where that row is drawn.
//
// The menu needs a keyboard route because right-click is not reliably
// deliverable: a terminal emulator may keep the right button for its own
// context menu and never forward button 3 to the application at all (iTerm2
// does this by default), and inside tmux the event has to survive a second hop.
// A menu reachable only by right-click is, on those setups, a menu that does
// not exist.
func (m Model) openMenuAtSelection() (tea.Model, tea.Cmd) {
	if !m.ready || m.width < minWidth || m.height < m.minHeight() {
		return m, nil
	}
	g := m.geometry()
	top, bottom := g.boxes[m.focus].listRows()

	cur := m.cur[m.focus]
	start, _ := viewport(cur, m.panelLen(m.focus), bottom-top)
	y := top + (cur - start)
	if y < top {
		y = top
	}
	if y >= bottom {
		y = bottom - 1
	}

	// Indent it into the panel so the box hangs off the row rather than off the
	// screen edge, the way it would from a click.
	opened, ok := m.openMenu(m.focus, g.leftW/3, y)
	if !ok {
		return m, nil
	}
	return opened, nil
}

func (m Model) closeMenu() Model {
	m.mode = modeNormal
	m.menu = nil
	m.menuCur = 0
	return m
}

// runMenuEntry dispatches the selected entry to the same handlers the keyboard
// uses. Each acts on the focused panel's selection, which rightClick has
// already pointed at the clicked row.
func (m Model) runMenuEntry() (tea.Model, tea.Cmd) {
	if m.menuCur < 0 || m.menuCur >= len(m.menu) {
		return m.closeMenu(), nil
	}
	op := m.menu[m.menuCur].op
	m = m.closeMenu()
	switch op {
	case menuAttach:
		return m.attachToSelection()
	case menuStart:
		// Not startProject directly: the menu is always opened on the focused
		// panel, and "start" means the project on Projects and the stopped row
		// on Sessions. activateSelection is the one place that knows which.
		return m.activateSelection()
	case menuEdit:
		return m.editProject()
	case menuRename:
		return m.startRename()
	case menuNewWindow:
		return m.startNewWindow()
	case menuSwapUp:
		return m.swapWindow(-1)
	case menuSwapDown:
		return m.swapWindow(1)
	case menuMoveWindow:
		return m.startMoveWindow()
	case menuSplitV:
		return m.startSplitPane(false)
	case menuSplitH:
		return m.startSplitPane(true)
	case menuLayout:
		return m.cycleLayout()
	case menuZoom:
		return m.zoomPane()
	case menuKill:
		return m.startKill()
	}
	return m, nil
}

// handleMenuKey drives the open menu from the keyboard, so a menu opened by the
// mouse doesn't have to be finished with it.
func (m Model) handleMenuKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		return m.closeMenu(), nil
	case "up", "k":
		m.menuCur = wrap(m.menuCur-1, len(m.menu))
		return m, nil
	case "down", "j":
		m.menuCur = wrap(m.menuCur+1, len(m.menu))
		return m, nil
	case "enter":
		return m.runMenuEntry()
	}
	return m, nil
}

// handleMenuMouse drives the open menu with the mouse: hover highlights, a
// click on an entry runs it, and a click anywhere else dismisses the menu.
func (m Model) handleMenuMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	idx, inside := m.menuEntryAt(msg.X, msg.Y)

	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.menuCur = wrap(m.menuCur-1, len(m.menu))
		return m, nil
	case tea.MouseButtonWheelDown:
		m.menuCur = wrap(m.menuCur+1, len(m.menu))
		return m, nil
	case tea.MouseButtonNone:
		// Motion: track the pointer so the highlight follows it.
		if inside && idx >= 0 {
			m.menuCur = idx
		}
		return m, nil
	case tea.MouseButtonLeft, tea.MouseButtonRight:
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		if !inside {
			return m.closeMenu(), nil
		}
		if idx < 0 {
			// The menu's own border: keep it open rather than treating a click
			// on its edge as a dismissal.
			return m, nil
		}
		m.menuCur = idx
		return m.runMenuEntry()
	}
	return m, nil
}

// menuBox is where the menu is drawn: its outer rectangle, clamped so the whole
// box stays on screen no matter where the click landed. The renderer and the
// hit test both read it, for the same reason panelBox exists.
func (m Model) menuBox() (x, y, w, h int) {
	w = menuWidth(m.menu) + borderSize
	h = len(m.menu) + borderSize

	// Offset by one so the box hangs below-right of the pointer, like a desktop
	// context menu, rather than sitting under it.
	x, y = m.menuX+1, m.menuY+1
	if x+w > m.width {
		x = m.width - w
	}
	if y+h > m.height {
		// Flip above the pointer when there's no room below.
		y = m.menuY - h
	}
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return x, y, w, h
}

// menuEntryAt maps a screen cell to a menu entry. It returns whether the cell
// is inside the menu box at all — a click outside dismisses, a click on the
// border does not.
func (m Model) menuEntryAt(px, py int) (idx int, inside bool) {
	if m.mode != modeMenu || len(m.menu) == 0 {
		return -1, false
	}
	x, y, w, h := m.menuBox()
	if px < x || px >= x+w || py < y || py >= y+h {
		return -1, false
	}
	row := py - (y + 1) // skip the top border
	if row < 0 || row >= len(m.menu) || px == x || px == x+w-1 {
		return -1, true
	}
	return row, true
}

// menuWidth is the inner width of the menu box: the widest "label  key" pair,
// plus a gap column either side.
func menuWidth(entries []menuEntry) int {
	labelW, keyW := 0, 0
	for _, e := range entries {
		if w := lipgloss.Width(e.label); w > labelW {
			labelW = w
		}
		if w := lipgloss.Width(e.key); w > keyW {
			keyW = w
		}
	}
	return labelW + keyW + menuGap + 2*menuPad
}

const (
	menuGap = 2 // between an entry's label and its key hint
	menuPad = 1 // blank column inside each vertical border
)

// renderMenu draws the menu box.
func (m Model) renderMenu() string {
	inner := menuWidth(m.menu)
	keyW := 0
	for _, e := range m.menu {
		if w := lipgloss.Width(e.key); w > keyW {
			keyW = w
		}
	}
	labelW := inner - keyW - menuGap - 2*menuPad

	lines := make([]string, 0, len(m.menu))
	for i, e := range m.menu {
		pad := strings.Repeat(" ", menuPad)
		label := padRight(truncate(e.label, labelW), labelW)
		gap := strings.Repeat(" ", menuGap)
		key := padRight(e.key, keyW)
		if i == m.menuCur {
			// Style the whole row as one run. Composing a selected row out of
			// separately-styled spans is what renderRow had to work around:
			// lipgloss terminates every run with a full SGR reset, so the
			// highlight would stop at the first styled segment.
			lines = append(lines, menuSelected.Render(pad+label+gap+key+pad))
			continue
		}
		lines = append(lines, menuItem.Render(pad+label+gap)+menuKey.Render(key)+menuItem.Render(pad))
	}
	return menuBorder.Width(inner).Render(strings.Join(lines, "\n"))
}

// wrap moves an index cyclically through n items, so the menu's ends join up.
func wrap(i, n int) int {
	if n == 0 {
		return 0
	}
	return ((i % n) + n) % n
}

// overlay composites box onto base with its top-left corner at (x, y),
// replacing the cells it covers rather than shifting them.
//
// It works in terminal cells, not bytes: every slice is made with the ANSI-aware
// helpers, because the base frame is full of color escapes and cutting one in
// half would spray its escape codes across the screen. Each spliced row is
// bracketed with resets so the box's own styling can't leak into the frame on
// either side of it.
func overlay(base, box string, x, y int) string {
	if x < 0 || y < 0 {
		return base
	}
	baseLines := strings.Split(base, "\n")
	boxLines := strings.Split(box, "\n")

	for i, boxLine := range boxLines {
		row := y + i
		if row < 0 || row >= len(baseLines) {
			continue
		}
		line := baseLines[row]
		lineW := ansi.StringWidth(line)
		// A frame line can be shorter than the click point (trailing blanks are
		// not padded out); extend it so the box still lands where it should.
		if lineW < x {
			line += strings.Repeat(" ", x-lineW)
			lineW = x
		}
		boxW := ansi.StringWidth(boxLine)

		left := ansi.Truncate(line, x, "")
		right := ""
		if x+boxW < lineW {
			right = ansi.TruncateLeft(line, x+boxW, "")
		}
		baseLines[row] = left + ansi.ResetStyle + boxLine + ansi.ResetStyle + right
	}
	return strings.Join(baseLines, "\n")
}
