package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// Layout constants. The left column holds the three stacked list panels; the
// rest of the width is the preview. Each bordered box costs 2 columns/rows of
// border plus one interior title row.
const (
	minLeftWidth = 24
	maxLeftWidth = 40
	helpHeight   = 1
	borderSize   = 2 // left+right or top+bottom border
	titleRows    = 1
)

var (
	accentColor = lipgloss.Color("6")   // cyan: focused border + active markers
	subtleColor = lipgloss.Color("240") // dim gray: blurred borders, hints

	focusedBorder = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accentColor)
	blurredBorder = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(subtleColor)

	focusedTitle = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	blurredTitle = lipgloss.NewStyle().Bold(true).Foreground(subtleColor)

	selectedRow = lipgloss.NewStyle().Reverse(true)
	hintStyle   = lipgloss.NewStyle().Foreground(subtleColor)
	helpStyle   = lipgloss.NewStyle().Foreground(subtleColor)
	modalStyle  = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	keyStyle    = lipgloss.NewStyle().Bold(true)
)

// View renders the whole TUI frame.
func (m Model) View() string {
	if !m.ready || m.width < minLeftWidth+20 || m.height < 8 {
		return "wyrm: terminal too small"
	}

	if m.mode == modeHelp {
		return m.renderHelpOverlay()
	}

	leftW := m.width * 30 / 100
	if leftW < minLeftWidth {
		leftW = minLeftWidth
	}
	if leftW > maxLeftWidth {
		leftW = maxLeftWidth
	}
	rightW := m.width - leftW

	bodyH := m.height - helpHeight
	// Distribute body height across the left panels, remainder to the last.
	panelH := bodyH / int(numPanels)
	heights := []int{panelH, panelH, panelH, bodyH - 3*panelH}

	left := lipgloss.JoinVertical(
		lipgloss.Left,
		m.renderProjects(leftW, heights[0]),
		m.renderSessions(leftW, heights[1]),
		m.renderWindows(leftW, heights[2]),
		m.renderPanes(leftW, heights[3]),
	)
	right := m.renderPreview(rightW, bodyH)
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)

	return lipgloss.JoinVertical(lipgloss.Left, body, m.renderHelp())
}

func (m Model) renderProjects(outerW, outerH int) string {
	rows := make([]string, len(m.projects))
	for i, p := range m.projects {
		mark := " "
		if p.Running {
			mark = "●"
		}
		rows[i] = fmt.Sprintf("%s %s", mark, p.Name)
	}
	return m.renderPanel(panelProjects, "Projects", rows, m.projectCur, outerW, outerH, "no wyrm configs found")
}

func (m Model) renderSessions(outerW, outerH int) string {
	rows := make([]string, len(m.sessions))
	for i, s := range m.sessions {
		mark := " "
		if s.Attached {
			mark = "●"
		}
		rows[i] = fmt.Sprintf("%s %s (%dw)", mark, s.Name, s.Windows)
	}
	return m.renderPanel(panelSessions, "Sessions", rows, m.sessionCur, outerW, outerH, "no running sessions")
}

func (m Model) renderWindows(outerW, outerH int) string {
	rows := make([]string, len(m.windows))
	for i, w := range m.windows {
		name := w.Name
		if name == "" {
			name = fmt.Sprintf("window %d", w.Index)
		}
		rows[i] = fmt.Sprintf("%d: %s", w.Index, name)
	}
	return m.renderPanel(panelWindows, "Windows", rows, m.windowCur, outerW, outerH, "")
}

func (m Model) renderPanes(outerW, outerH int) string {
	rows := make([]string, len(m.panes))
	for i, p := range m.panes {
		rows[i] = fmt.Sprintf("%s %s", p.ID, p.Command)
	}
	return m.renderPanel(panelPanes, "Panes", rows, m.paneCur, outerW, outerH, "")
}

// renderPanel draws one bordered list box with a title, a cursor-tracking
// viewport, and an empty-state hint.
func (m Model) renderPanel(p panel, title string, rows []string, cursor, outerW, outerH int, empty string) string {
	focused := m.focus == p
	innerW := outerW - borderSize
	innerH := outerH - borderSize
	listH := innerH - titleRows
	if listH < 1 {
		listH = 1
	}

	var b strings.Builder
	if focused {
		b.WriteString(focusedTitle.Render(truncate(title, innerW)))
	} else {
		b.WriteString(blurredTitle.Render(truncate(title, innerW)))
	}
	b.WriteByte('\n')

	if len(rows) == 0 {
		b.WriteString(hintStyle.Render(truncate(empty, innerW)))
		for i := 1; i < listH; i++ {
			b.WriteByte('\n')
		}
	} else {
		start, end := viewport(cursor, len(rows), listH)
		for i := start; i < end; i++ {
			line := truncate(rows[i], innerW)
			if i == cursor && focused {
				line = selectedRow.Render(padRight(line, innerW))
			}
			b.WriteString(line)
			if i < end-1 {
				b.WriteByte('\n')
			}
		}
		for i := end - start; i < listH; i++ {
			b.WriteByte('\n')
		}
	}

	box := blurredBorder
	if focused {
		box = focusedBorder
	}
	return box.Width(innerW).Height(innerH).Render(b.String())
}

func (m Model) renderPreview(outerW, outerH int) string {
	innerW := outerW - borderSize
	innerH := outerH - borderSize
	bodyH := innerH - titleRows
	if bodyH < 1 {
		bodyH = 1
	}

	title := m.previewTitle
	if title == "" {
		title = "Preview"
	}

	var b strings.Builder
	b.WriteString(blurredTitle.Render(truncate(title, innerW)))
	b.WriteByte('\n')

	content := m.preview
	if content == "" && m.err != nil {
		content = "error: " + m.err.Error()
	}
	lines := strings.Split(content, "\n")
	if len(lines) > bodyH {
		lines = lines[:bodyH]
	}
	for i := 0; i < bodyH; i++ {
		if i < len(lines) {
			b.WriteString(truncate(lines[i], innerW))
		}
		if i < bodyH-1 {
			b.WriteByte('\n')
		}
	}

	return blurredBorder.Width(innerW).Height(innerH).Render(b.String())
}

func (m Model) renderHelp() string {
	switch m.mode {
	case modeConfirm:
		return modalStyle.Render(truncate(m.confirmPrompt, m.width))
	case modePrompt:
		line := m.promptTitle + " " + m.textInput.View()
		return modalStyle.Render(truncate(line, m.width))
	}
	keys := m.helpKeys()
	return helpStyle.Render(truncate(keys, m.width))
}

// helpSection is one titled group of key bindings in the full help overlay.
type helpSection struct {
	title   string
	entries [][2]string // {keys, description}
}

// helpSections is the complete keyboard reference shown by the "?" overlay.
var helpSections = []helpSection{
	{"Global", [][2]string{
		{"Tab / Shift-Tab", "cycle focus between panels"},
		{"1 / 2 / 3 / 4", "jump to Projects / Sessions / Windows / Panes"},
		{"↑ ↓  or  j k", "move the selection"},
		{"R", "reload the project and session lists"},
		{"?", "toggle this help"},
		{"q / Ctrl-C", "quit"},
	}},
	{"Projects panel", [][2]string{
		{"Enter", "start or attach the config's session"},
		{"e", "edit the config in $EDITOR"},
		{"x", "stop the session (runs on_project_exit)"},
	}},
	{"Sessions panel", [][2]string{
		{"Enter", "attach (or switch-client inside tmux)"},
		{"x", "kill the session"},
		{"r", "rename the session"},
	}},
	{"Windows panel", [][2]string{
		{"Enter", "attach, landing on this window"},
		{"x", "kill the window"},
		{"r", "rename the window"},
		{"n", "new window"},
		{"L", "cycle the window layout"},
	}},
	{"Panes panel", [][2]string{
		{"Enter", "attach, landing on this pane"},
		{"x", "kill the pane"},
		{"z", "toggle zoom"},
	}},
	{"Confirm / prompt", [][2]string{
		{"y / Enter", "confirm"},
		{"n / Esc", "cancel"},
	}},
}

// helpColumn renders a set of sections into one aligned block of lines: a
// styled section header followed by its "key  description" rows, blank line
// between sections.
func helpColumn(sections []helpSection) string {
	keyCol := 0
	for _, s := range sections {
		for _, e := range s.entries {
			if w := lipgloss.Width(e[0]); w > keyCol {
				keyCol = w
			}
		}
	}
	var lines []string
	for i, s := range sections {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, blurredTitle.Render(s.title))
		for _, e := range s.entries {
			lines = append(lines, "  "+keyStyle.Render(padRight(e[0], keyCol))+"  "+e[1])
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// helpLines returns the body of the help overlay as individual lines (so it can
// be scrolled). It lays the sections out in two side-by-side columns when the
// terminal is wide enough, and falls back to a single column otherwise.
func (m Model) helpLines() []string {
	half := (len(helpSections) + 1) / 2
	two := lipgloss.JoinHorizontal(lipgloss.Top,
		helpColumn(helpSections[:half]), "    ", helpColumn(helpSections[half:]))
	if lipgloss.Width(two) <= m.width-helpChrome {
		return strings.Split(two, "\n")
	}
	return strings.Split(helpColumn(helpSections), "\n")
}

// helpChrome is the horizontal space the overlay's border + padding consume.
const helpChrome = 4 // border (2) + padding (2)

// helpVisible is how many body lines fit between the title and footer.
func (m Model) helpVisible() int {
	// border (2) + title (1) + footer (1).
	v := m.height - 4
	if v < 1 {
		v = 1
	}
	return v
}

func (m Model) helpMaxScroll() int {
	maxScroll := len(m.helpLines()) - m.helpVisible()
	if maxScroll < 0 {
		return 0
	}
	return maxScroll
}

// renderHelpOverlay draws a centered cheat sheet of every binding. When the
// content is taller than the terminal it shows a scrollable window with a
// position indicator instead of overflowing off-screen.
func (m Model) renderHelpOverlay() string {
	lines := m.helpLines()
	visible := m.helpVisible()

	scroll := m.helpScroll
	if maxScroll := len(lines) - visible; scroll > maxScroll {
		scroll = maxScroll
	}
	if scroll < 0 {
		scroll = 0
	}
	end := scroll + visible
	if end > len(lines) {
		end = len(lines)
	}

	title := focusedTitle.Render("wyrm — keyboard shortcuts")
	body := lipgloss.JoinVertical(lipgloss.Left, lines[scroll:end]...)

	var footer string
	if len(lines) > visible {
		pct := 100
		if maxScroll := len(lines) - visible; maxScroll > 0 {
			pct = scroll * 100 / maxScroll
		}
		footer = hintStyle.Render(fmt.Sprintf("%d%%  ·  ↑↓/jk scroll  ·  esc close", pct))
	} else {
		footer = hintStyle.Render("esc close")
	}

	box := focusedBorder.Padding(0, 1).Render(lipgloss.JoinVertical(lipgloss.Left, title, body, footer))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

// helpKeys returns the contextual key hints for the focused panel.
func (m Model) helpKeys() string {
	const nav = "tab/1-4: focus  jk: move  R: reload  ?: help  q: quit"
	switch m.focus {
	case panelProjects:
		return "↵: start/attach  e: edit  x: stop  " + nav
	case panelSessions:
		return "↵: attach  x: kill  r: rename  " + nav
	case panelWindows:
		return "↵: attach  x: kill  r: rename  n: new-win  L: layout  " + nav
	case panelPanes:
		return "↵: attach  x: kill  z: zoom  " + nav
	}
	return nav
}

// viewport returns the [start,end) slice of a list of length n that keeps
// cursor visible within a window of height rows.
func viewport(cursor, n, rows int) (int, int) {
	if n <= rows {
		return 0, n
	}
	start := cursor - rows/2
	if start < 0 {
		start = 0
	}
	if start+rows > n {
		start = n - rows
	}
	return start, start + rows
}

// truncate clips s to a display width of w columns, appending "…" when cut.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= w {
		return s
	}
	return runewidth.Truncate(s, w, "…")
}

// padRight pads s with spaces to a display width of w so a reverse-video
// selected row fills the panel width.
func padRight(s string, w int) string {
	gap := w - runewidth.StringWidth(s)
	if gap <= 0 {
		return s
	}
	return s + strings.Repeat(" ", gap)
}
