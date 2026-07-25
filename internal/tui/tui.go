// Package tui implements wyrm's interactive, full-screen session manager
// (wyrm tui): a lazygit-style multi-panel view over the running tmux world.
//
// Four stacked panels on the left (Projects -> Sessions -> Windows -> Panes)
// drive a live pane-content preview on the right. Enter attaches to the
// selected session, and management actions (kill/rename/new-window, plus
// building or editing a project's config) are available directly from the
// relevant panel.
//
// The model follows the repo convention of taking a tmux.Runner rather than
// shelling out directly, so Update stays pure and unit-testable: every tmux
// call happens inside a tea.Cmd closure that captures the Runner, and Update
// only ever reacts to the resulting messages.
package tui

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jskoll/wyrm/internal/config"
	"github.com/jskoll/wyrm/internal/picker"
	"github.com/jskoll/wyrm/internal/tmux"
)

// refreshInterval is how often the selected pane's preview is re-captured so
// the view stays live.
const refreshInterval = time.Second

// listRefreshInterval is how often the project and session lists are re-read.
// Slower than the preview because it costs a tmux round trip per level of the
// cascade, but not never: a session manager that showed a live preview of a
// pane while its session list went stale — including sessions started or ended
// in another terminal — had the two exactly backwards.
const listRefreshInterval = 3 * time.Second

// selfPreviewNotice replaces the live capture when the selected pane is the one
// wyrm tui is itself running in: capturing it would render the TUI back into
// its own preview, a mirror-of-a-mirror that thrashes on every refresh.
const selfPreviewNotice = "This pane is running wyrm tui.\nPreview hidden to avoid a feedback loop."

// panel identifies one of the focusable left-column list panels.
type panel int

const (
	panelProjects panel = iota
	panelSessions
	panelWindows
	panelPanes
	numPanels
)

// previewSource tracks what the main panel is currently showing, so the ticker
// only refreshes a live pane capture (not a static config preview).
type previewSource int

const (
	previewPane previewSource = iota
	previewConfig
)

// mode is the input mode: normal navigation, a yes/no confirm modal (for
// destructive kills), a text-entry prompt (rename / new-window), the help
// overlay, or type-to-filter.
type mode int

const (
	modeNormal mode = iota
	modeConfirm
	modePrompt
	modeHelp
	modeFilter
)

// Model is the Bubble Tea model for the TUI. It is a plain value type; Update
// returns an updated copy. tmux access is confined to the command closures, so
// the model itself holds only data and view state.
type Model struct {
	runner   tmux.Runner
	settings *config.Settings

	focus panel

	projects []Project
	sessions []picker.Session
	windows  []tmux.WindowInfo
	panes    []tmux.PaneInfo

	projectCur int
	sessionCur int
	windowCur  int
	paneCur    int

	preview      string
	previewTitle string
	previewSrc   previewSource

	width, height int
	ready         bool

	// selfPane is the tmux pane ID wyrm tui is running in ($TMUX_PANE), or ""
	// when launched outside tmux. Its preview is suppressed to avoid a mirror
	// loop.
	selfPane string

	// modal state.
	mode          mode
	pending       pendingAction
	confirmPrompt string          // shown in modeConfirm
	promptTitle   string          // label shown in modePrompt
	textInput     textinput.Model // active in modePrompt
	helpScroll    int             // top line offset of the help overlay (modeHelp)

	// layoutIdx rotates through cycleLayouts on "L", and layoutWindow is the
	// window it belongs to. Keeping them together makes the cycle per-window:
	// a single shared index meant switching windows resumed mid-cycle, so the
	// first "L" often re-applied the layout that window already had and looked
	// like it had done nothing.
	layoutIdx    int
	layoutWindow string

	// filter narrows the focused panel (modeFilter types into it). Only the
	// focused panel is filtered, so the rest of the cascade stays readable.
	filter    string
	filtering bool

	err error

	// pendingAttach is the tmux session ID (e.g. "$3") to hand the terminal to
	// once the program exits. The alt-screen program can't attach in-place, so
	// runTUI performs the attach after Run returns — mirroring runPicker.
	pendingAttach string
}

// New builds a Model backed by runner. windowCur/paneCur start at -1 so the
// first load of each snaps to the active window/pane rather than index 0.
// Focus starts on Sessions rather than the first panel: the common reason to
// open the TUI is to reach a running session, and it makes the initial preview
// a live pane capture instead of a config file.
// settings may be nil (the Projects panel then lists only local configs).
func New(runner tmux.Runner, settings *config.Settings) Model {
	return Model{
		runner:    runner,
		settings:  settings,
		focus:     panelSessions,
		windowCur: -1,
		paneCur:   -1,
		selfPane:  os.Getenv("TMUX_PANE"),
	}
}

// Init loads the initial project and session lists and starts the refresh
// ticker.
func (m Model) Init() tea.Cmd {
	return tea.Batch(loadProjects(m.runner, m.settings), loadSessions(m.runner), tick(), listTick())
}

// --- messages ---

type sessionsMsg struct {
	sessions []picker.Session
	err      error
}

type windowsMsg struct {
	sessionID string
	windows   []tmux.WindowInfo
	err       error
}

type panesMsg struct {
	windowID string
	panes    []tmux.PaneInfo
	err      error
}

type previewMsg struct {
	paneID  string
	content string
	err     error
}

type tickMsg time.Time

// listTickMsg drives the slower project/session refresh — see
// listRefreshInterval.
type listTickMsg time.Time

// --- commands ---

func loadSessions(r tmux.Runner) tea.Cmd {
	return func() tea.Msg {
		s, err := picker.ListSessions(r)
		return sessionsMsg{sessions: s, err: err}
	}
}

func loadWindows(r tmux.Runner, sessionID string) tea.Cmd {
	return func() tea.Msg {
		w, err := tmux.ListWindows(r, sessionID)
		return windowsMsg{sessionID: sessionID, windows: w, err: err}
	}
}

func loadPanes(r tmux.Runner, windowID string) tea.Cmd {
	return func() tea.Msg {
		p, err := tmux.ListPanes(r, windowID)
		return panesMsg{windowID: windowID, panes: p, err: err}
	}
}

func loadPreview(r tmux.Runner, paneID string) tea.Cmd {
	return func() tea.Msg {
		out, err := tmux.CapturePane(r, paneID)
		return previewMsg{paneID: paneID, content: out, err: err}
	}
}

func tick() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func listTick() tea.Cmd {
	return tea.Tick(listRefreshInterval, func(t time.Time) tea.Msg { return listTickMsg(t) })
}

// --- filtering ---

// filterFor returns the filter text in force for a panel. Only the focused
// panel is filtered: narrowing every panel at once would hide the session a
// window belongs to, which is exactly the context the cascade exists to show.
func (m Model) filterFor(p panel) string {
	if m.focus != p {
		return ""
	}
	return m.filter
}

// visible* return the rows a panel actually displays. Cursors index these, not
// the unfiltered slices, so the selection always means what's under it.

func (m Model) visibleProjects() []Project {
	f := m.filterFor(panelProjects)
	if f == "" {
		return m.projects
	}
	out := make([]Project, 0, len(m.projects))
	for _, p := range m.projects {
		if _, ok := picker.FuzzyMatch(f, p.Name); ok {
			out = append(out, p)
		}
	}
	return out
}

func (m Model) visibleSessions() []picker.Session {
	f := m.filterFor(panelSessions)
	if f == "" {
		return m.sessions
	}
	out := make([]picker.Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		if _, ok := picker.FuzzyMatch(f, s.Name); ok {
			out = append(out, s)
		}
	}
	return out
}

func (m Model) visibleWindows() []tmux.WindowInfo {
	f := m.filterFor(panelWindows)
	if f == "" {
		return m.windows
	}
	out := make([]tmux.WindowInfo, 0, len(m.windows))
	for _, w := range m.windows {
		if _, ok := picker.FuzzyMatch(f, w.Name); ok {
			out = append(out, w)
		}
	}
	return out
}

func (m Model) visiblePanes() []tmux.PaneInfo {
	f := m.filterFor(panelPanes)
	if f == "" {
		return m.panes
	}
	out := make([]tmux.PaneInfo, 0, len(m.panes))
	for _, p := range m.panes {
		if _, ok := picker.FuzzyMatch(f, p.ID+" "+p.Command); ok {
			out = append(out, p)
		}
	}
	return out
}

// panelLen is the number of rows currently displayed in a panel.
func (m Model) panelLen(p panel) int {
	switch p {
	case panelProjects:
		return len(m.visibleProjects())
	case panelSessions:
		return len(m.visibleSessions())
	case panelWindows:
		return len(m.visibleWindows())
	case panelPanes:
		return len(m.visiblePanes())
	}
	return 0
}

// --- selection accessors ---

func (m Model) currentProject() (Project, bool) {
	list := m.visibleProjects()
	if m.projectCur < 0 || m.projectCur >= len(list) {
		return Project{}, false
	}
	return list[m.projectCur], true
}

func (m Model) currentSession() (picker.Session, bool) {
	list := m.visibleSessions()
	if m.sessionCur < 0 || m.sessionCur >= len(list) {
		return picker.Session{}, false
	}
	return list[m.sessionCur], true
}

func (m Model) currentWindow() (tmux.WindowInfo, bool) {
	list := m.visibleWindows()
	if m.windowCur < 0 || m.windowCur >= len(list) {
		return tmux.WindowInfo{}, false
	}
	return list[m.windowCur], true
}

func (m Model) currentPane() (tmux.PaneInfo, bool) {
	list := m.visiblePanes()
	if m.paneCur < 0 || m.paneCur >= len(list) {
		return tmux.PaneInfo{}, false
	}
	return list[m.paneCur], true
}

// --- update ---

// Update is the pure reducer. It never touches tmux or stdio directly; it only
// folds incoming messages into new state and returns follow-up commands.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tickMsg:
		// Refresh the live pane preview, then reschedule. A config preview is
		// static, so leave it alone — keyed off what's actually being shown
		// rather than off which panel has focus.
		var cmd tea.Cmd
		if m.previewSrc == previewPane {
			cmd = m.reloadPreview()
		}
		return m, tea.Batch(cmd, tick())

	case listTickMsg:
		// Re-read the lists so sessions started or ended elsewhere show up.
		// Held off while a modal is open, so the data under a confirm prompt
		// can't change between reading it and answering it.
		if m.mode != modeNormal && m.mode != modeFilter {
			return m, listTick()
		}
		return m, tea.Batch(loadProjects(m.runner, m.settings), loadSessions(m.runner), listTick())

	case projectsMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.projects = msg.projects
		m.projectCur = clamp(m.projectCur, m.panelLen(panelProjects))
		if m.focus == panelProjects {
			return m, m.updatePreview()
		}
		return m, nil

	case configPreviewMsg:
		if m.previewSrc != previewConfig {
			return m, nil
		}
		if p, ok := m.currentProject(); !ok || p.Path != msg.path {
			return m, nil
		}
		if msg.err != nil {
			m.preview = msg.err.Error()
			return m, nil
		}
		m.preview = msg.content
		return m, nil

	case projectStartedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.pendingAttach = msg.sessionID
		return m, tea.Quit

	case sessionsMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		m.sessions = msg.sessions
		m.sessionCur = clamp(m.sessionCur, m.panelLen(panelSessions))
		return m, m.reloadWindows()

	case windowsMsg:
		// Ignore a stale response for a session we've since moved off of.
		if s, ok := m.currentSession(); !ok || s.ID != msg.sessionID {
			return m, nil
		}
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.windows = msg.windows
		m.windowCur = activeOrClamp(m.windowCur, m.visibleWindows())
		return m, m.reloadPanes()

	case panesMsg:
		if w, ok := m.currentWindow(); !ok || w.ID != msg.windowID {
			return m, nil
		}
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.panes = msg.panes
		m.paneCur = activePaneOrClamp(m.paneCur, m.visiblePanes())
		return m, m.reloadPreview()

	case previewMsg:
		if m.previewSrc != previewPane {
			return m, nil
		}
		if p, ok := m.currentPane(); !ok || p.ID != msg.paneID {
			return m, nil
		}
		if msg.err != nil {
			m.preview = msg.err.Error()
			return m, nil
		}
		m.preview = msg.content
		if p, ok := m.currentPane(); ok {
			m.previewTitle = p.ID + " " + p.Command
		}
		return m, nil

	case actionErrMsg:
		m.err = msg.err
		return m, nil
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeConfirm:
		return m.handleConfirmKey(msg)
	case modePrompt:
		return m.handlePromptKey(msg)
	case modeHelp:
		return m.handleHelpKey(msg)
	case modeFilter:
		return m.handleFilterKey(msg)
	}
	// Any key clears a reported error, so the footer returns to the key hints
	// once it's been seen.
	m.err = nil
	return m.handleNormalKey(msg)
}

// handleFilterKey types into the panel filter. The selection is clamped on
// every keystroke, since narrowing the list can put the cursor past its end.
func (m Model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.filtering = false
		m.mode = modeNormal
		return m, nil
	case tea.KeyEsc, tea.KeyCtrlC:
		m.filtering = false
		m.filter = ""
		m.mode = modeNormal
		return m.clampFocused()
	case tea.KeyBackspace:
		if m.filter != "" {
			m.filter = m.filter[:len(m.filter)-1]
		}
		return m.clampFocused()
	case tea.KeyRunes, tea.KeySpace:
		m.filter += string(msg.Runes)
		if msg.Type == tea.KeySpace {
			m.filter += " "
		}
		return m.clampFocused()
	}
	return m, nil
}

// clampFocused keeps the focused panel's cursor inside its visible list and
// reloads whatever depends on it.
func (m Model) clampFocused() (tea.Model, tea.Cmd) {
	n := m.panelLen(m.focus)
	switch m.focus {
	case panelProjects:
		m.projectCur = clamp(m.projectCur, n)
		return m, m.updatePreview()
	case panelSessions:
		m.sessionCur = clamp(m.sessionCur, n)
		m.windowCur, m.paneCur = -1, -1
		return m, m.reloadWindows()
	case panelWindows:
		m.windowCur = clamp(m.windowCur, n)
		m.paneCur = -1
		return m, m.reloadPanes()
	case panelPanes:
		m.paneCur = clamp(m.paneCur, n)
		return m, m.reloadPreview()
	}
	return m, nil
}

// handleHelpKey scrolls the help overlay or closes it. Only esc/q/?/Ctrl-C
// close; navigation keys scroll so a taller-than-screen cheat sheet stays fully
// reachable.
func (m Model) handleHelpKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "?", "ctrl+c":
		m.mode = modeNormal
		m.helpScroll = 0
		return m, nil
	case "down", "j":
		m.helpScroll++
	case "up", "k":
		m.helpScroll--
	case "pgdown", "ctrl+d", "f", " ":
		m.helpScroll += m.helpVisible() - 1
	case "pgup", "ctrl+u", "b":
		m.helpScroll -= m.helpVisible() - 1
	case "g", "home":
		m.helpScroll = 0
	case "G", "end":
		m.helpScroll = m.helpMaxScroll()
	}
	if maxScroll := m.helpMaxScroll(); m.helpScroll > maxScroll {
		m.helpScroll = maxScroll
	}
	if m.helpScroll < 0 {
		m.helpScroll = 0
	}
	return m, nil
}

func (m Model) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "R":
		return m, tea.Batch(loadProjects(m.runner, m.settings), loadSessions(m.runner))
	case "tab", "l", "right":
		m.focus = (m.focus + 1) % numPanels
		return m, m.updatePreview()
	case "shift+tab", "h", "left":
		m.focus = (m.focus + numPanels - 1) % numPanels
		return m, m.updatePreview()
	case "1":
		m.focus = panelProjects
		return m, m.updatePreview()
	case "2":
		m.focus = panelSessions
		return m, m.updatePreview()
	case "3":
		m.focus = panelWindows
		return m, m.updatePreview()
	case "4":
		m.focus = panelPanes
		return m, m.updatePreview()
	case "up", "k":
		return m.moveCursor(-1)
	case "down", "j":
		return m.moveCursor(1)
	case "pgup", "ctrl+u":
		return m.moveCursor(-m.pageSize())
	case "pgdown", "ctrl+d":
		return m.moveCursor(m.pageSize())
	case "g", "home":
		return m.moveCursorTo(0)
	case "G", "end":
		return m.moveCursorTo(m.panelLen(m.focus) - 1)
	case "/":
		m.mode = modeFilter
		m.filtering = true
		return m, nil
	case "esc":
		if m.filter != "" {
			m.filter = ""
			return m.clampFocused()
		}
		return m, nil
	case "enter":
		if m.focus == panelProjects {
			return m.startProject()
		}
		return m.attachToSelection()
	case "x":
		return m.startKill()
	case "r":
		return m.startRename()
	case "n":
		// Scoped to the panels whose footer advertises it. These used to fire
		// from any panel, acting on a selection in a panel that didn't have
		// focus while the contextual help said otherwise.
		if m.focus != panelSessions && m.focus != panelWindows {
			return m, nil
		}
		return m.startNewWindow()
	case "L":
		if m.focus != panelWindows {
			return m, nil
		}
		return m.cycleLayout()
	case "z":
		if m.focus != panelPanes {
			return m, nil
		}
		return m.zoomPane()
	case "e":
		if m.focus != panelProjects {
			return m, nil
		}
		return m.editProject()
	case "?":
		m.mode = modeHelp
		m.helpScroll = 0
		return m, nil
	}
	return m, nil
}

// pageSize is how far PgUp/PgDn move: one panel's worth of rows, less one for
// continuity. The exact panel height isn't known here, so use the share the
// layout would give it.
func (m Model) pageSize() int {
	n := (m.height - helpHeight) / int(numPanels)
	n -= borderSize + titleRows + 1
	if n < 1 {
		return 1
	}
	return n
}

// attachToSelection queues an attach to the current session, pre-selecting the
// focused window (and pane) so the client lands exactly where the cursor is.
func (m Model) attachToSelection() (tea.Model, tea.Cmd) {
	s, ok := m.currentSession()
	if !ok {
		return m, nil
	}
	m.pendingAttach = s.ID
	if w, ok := m.currentWindow(); ok {
		paneID := ""
		if p, ok := m.currentPane(); ok {
			paneID = p.ID
		}
		return m, tea.Sequence(selectTargetCmd(m.runner, w.ID, paneID), tea.Quit)
	}
	return m, tea.Quit
}

// startKill opens a confirm modal for the destructive kill appropriate to the
// focused panel.
func (m Model) startKill() (tea.Model, tea.Cmd) {
	switch m.focus {
	case panelProjects:
		p, ok := m.currentProject()
		if !ok || !p.Running {
			return m, nil
		}
		m.pending = pendingAction{op: opKillProject, path: p.Path}
		m.confirmPrompt = "Stop project '" + p.Name + "' (runs on_project_exit)?  (y/n)"
	case panelSessions:
		s, ok := m.currentSession()
		if !ok {
			return m, nil
		}
		m.pending = pendingAction{op: opKillSession, sessionID: s.ID}
		m.confirmPrompt = "Kill session '" + s.Name + "'?  (y/n)"
	case panelWindows:
		s, sok := m.currentSession()
		w, wok := m.currentWindow()
		if !sok || !wok {
			return m, nil
		}
		m.pending = pendingAction{op: opKillWindow, sessionID: s.ID, windowID: w.ID}
		m.confirmPrompt = "Kill window '" + w.Name + "'?  (y/n)"
	case panelPanes:
		w, wok := m.currentWindow()
		p, pok := m.currentPane()
		if !wok || !pok {
			return m, nil
		}
		m.pending = pendingAction{op: opKillPane, windowID: w.ID, paneID: p.ID}
		m.confirmPrompt = "Kill pane " + p.ID + " (" + p.Command + ")?  (y/n)"
	default:
		return m, nil
	}
	m.mode = modeConfirm
	return m, nil
}

// startRename opens a text prompt to rename the focused session or window.
func (m Model) startRename() (tea.Model, tea.Cmd) {
	switch m.focus {
	case panelSessions:
		s, ok := m.currentSession()
		if !ok {
			return m, nil
		}
		m.pending = pendingAction{op: opRenameSession, sessionID: s.ID}
		return m.openPrompt("Rename session:", s.Name)
	case panelWindows:
		s, sok := m.currentSession()
		w, wok := m.currentWindow()
		if !sok || !wok {
			return m, nil
		}
		m.pending = pendingAction{op: opRenameWindow, sessionID: s.ID, windowID: w.ID}
		return m.openPrompt("Rename window:", w.Name)
	}
	return m, nil
}

// startNewWindow opens a text prompt for a new window's name in the current
// session.
func (m Model) startNewWindow() (tea.Model, tea.Cmd) {
	s, ok := m.currentSession()
	if !ok {
		return m, nil
	}
	m.pending = pendingAction{op: opNewWindow, sessionID: s.ID}
	return m.openPrompt("New window name:", "")
}

func (m Model) openPrompt(title, initial string) (tea.Model, tea.Cmd) {
	ti := textinput.New()
	// Without a Width the input has no scroll window, so a value longer than
	// the footer pushed the cursor off the right edge of the screen with no
	// way to see what was being typed.
	if w := m.width - lipgloss.Width(title) - 2; w > 8 {
		ti.Width = w
	} else {
		ti.Width = 8
	}
	ti.SetValue(initial)
	ti.CursorEnd()
	cmd := ti.Focus()
	m.textInput = ti
	m.promptTitle = title
	m.mode = modePrompt
	return m, cmd
}

// cycleLayout advances the focused window through tmux's standard layouts.
func (m Model) cycleLayout() (tea.Model, tea.Cmd) {
	w, ok := m.currentWindow()
	if !ok {
		return m, nil
	}
	// Restart the cycle when the target window changes, so the first press on
	// a window always visibly changes something.
	if m.layoutWindow != w.ID {
		m.layoutWindow, m.layoutIdx = w.ID, -1
	}
	m.layoutIdx = (m.layoutIdx + 1) % len(cycleLayouts)
	return m, selectLayoutCmd(m.runner, w.ID, cycleLayouts[m.layoutIdx])
}

// zoomPane toggles zoom on the focused pane.
func (m Model) zoomPane() (tea.Model, tea.Cmd) {
	p, ok := m.currentPane()
	if !ok {
		return m, nil
	}
	return m, zoomPaneCmd(m.runner, p.ID)
}

// startProject builds-or-attaches the selected project's session and hands the
// terminal over (session.Create is idempotent, so this is start *and* attach).
func (m Model) startProject() (tea.Model, tea.Cmd) {
	p, ok := m.currentProject()
	if !ok {
		return m, nil
	}
	return m, startProjectCmd(m.runner, p.Path)
}

// editProject opens the selected project's config in $EDITOR.
func (m Model) editProject() (tea.Model, tea.Cmd) {
	p, ok := m.currentProject()
	if !ok {
		return m, nil
	}
	return m, editConfigCmd(m.runner, m.settings, p.Path)
}

func (m Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	// Deliberately not "enter": enter is attach in normal mode, so accepting
	// it here turned a reflexive x-then-enter into a killed session.
	case "y", "Y":
		cmd := m.executePending()
		m.mode = modeNormal
		m.pending = pendingAction{}
		return m, cmd
	case "n", "N", "esc", "ctrl+c":
		m.mode = modeNormal
		m.pending = pendingAction{}
		return m, nil
	}
	return m, nil
}

func (m Model) handlePromptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		value := m.textInput.Value()
		m.mode = modeNormal
		if value == "" {
			m.pending = pendingAction{}
			return m, nil
		}
		cmd := m.executePendingWithValue(value)
		m.pending = pendingAction{}
		return m, cmd
	case tea.KeyEsc, tea.KeyCtrlC:
		m.mode = modeNormal
		m.pending = pendingAction{}
		return m, nil
	}
	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

// moveCursor moves the selection in the focused panel by delta and reloads the
// dependent panels/preview.
func (m Model) moveCursor(delta int) (tea.Model, tea.Cmd) {
	return m.moveCursorTo(m.focusedCursor() + delta)
}

// focusedCursor is the focused panel's current index.
func (m Model) focusedCursor() int {
	switch m.focus {
	case panelProjects:
		return m.projectCur
	case panelSessions:
		return m.sessionCur
	case panelWindows:
		return m.windowCur
	case panelPanes:
		return m.paneCur
	}
	return 0
}

// moveCursorTo sets the focused panel's selection, clamping to the ends rather
// than refusing to move — a PgDn near the bottom should land on the last row,
// not do nothing — and reloads the panels below it.
func (m Model) moveCursorTo(next int) (tea.Model, tea.Cmd) {
	n := m.panelLen(m.focus)
	if n == 0 {
		return m, nil
	}
	if next < 0 {
		next = 0
	}
	if next >= n {
		next = n - 1
	}
	switch m.focus {
	case panelProjects:
		if next == m.projectCur {
			return m, nil
		}
		m.projectCur = next
		return m, m.updatePreview()
	case panelSessions:
		if next == m.sessionCur {
			return m, nil
		}
		m.sessionCur = next
		// Parent changed: snap the child selections to the new session's
		// active window/pane on reload.
		m.windowCur, m.paneCur = -1, -1
		return m, m.reloadWindows()
	case panelWindows:
		if next == m.windowCur {
			return m, nil
		}
		m.windowCur = next
		m.paneCur = -1
		return m, m.reloadPanes()
	case panelPanes:
		if next == m.paneCur {
			return m, nil
		}
		m.paneCur = next
		return m, m.reloadPreview()
	}
	return m, nil
}

func (m *Model) reloadWindows() tea.Cmd {
	s, ok := m.currentSession()
	if !ok {
		m.windows, m.panes = nil, nil
		m.preview, m.previewTitle = "", ""
		return nil
	}
	return loadWindows(m.runner, s.ID)
}

func (m *Model) reloadPanes() tea.Cmd {
	w, ok := m.currentWindow()
	if !ok {
		m.panes = nil
		m.preview, m.previewTitle = "", ""
		return nil
	}
	return loadPanes(m.runner, w.ID)
}

// updatePreview points the main panel at the right source for the current
// focus: the selected project's config file (Projects panel) or the selected
// pane's live capture (everywhere else).
func (m *Model) updatePreview() tea.Cmd {
	if m.focus == panelProjects {
		m.previewSrc = previewConfig
		p, ok := m.currentProject()
		if !ok {
			m.preview, m.previewTitle = "", ""
			return nil
		}
		m.previewTitle = p.Path
		return loadConfigPreview(p.Path)
	}
	m.previewSrc = previewPane
	return m.reloadPreview()
}

func (m *Model) reloadPreview() tea.Cmd {
	p, ok := m.currentPane()
	if !ok {
		m.preview, m.previewTitle = "", ""
		return nil
	}
	// Never capture the pane wyrm tui itself occupies — that mirrors the TUI
	// into its own preview and thrashes on every refresh.
	if m.selfPane != "" && p.ID == m.selfPane {
		m.previewTitle = p.ID + " " + p.Command
		m.preview = selfPreviewNotice
		return nil
	}
	return loadPreview(m.runner, p.ID)
}

// Run drives the program to completion and returns the tmux session ID to
// attach to (empty if the user quit without choosing). An error the model was
// still holding when the user quit is reported on stderr — the alt screen is
// gone by then, so the footer that would have shown it is too.
func Run(runner tmux.Runner, settings *config.Settings, stderr io.Writer) (pendingAttach string, err error) {
	// Before the alt screen: a broken theme file has to be reportable, and
	// styles are read by every render from here on.
	theme, err := LoadTheme()
	if err != nil {
		return "", err
	}
	SetTheme(theme)

	fm, err := tea.NewProgram(New(runner, settings), tea.WithAltScreen()).Run()
	if err != nil {
		return "", err
	}
	final, ok := fm.(Model)
	if !ok {
		return "", fmt.Errorf("unexpected final model %T", fm)
	}
	if final.err != nil {
		_, _ = fmt.Fprintf(stderr, "wyrm: %v\n", final.err)
	}
	return final.pendingAttach, nil
}

// --- small helpers ---

// clamp keeps cur within [0, n); returns 0 when the list is empty.
func clamp(cur, n int) int {
	if n == 0 {
		return 0
	}
	if cur < 0 {
		return 0
	}
	if cur >= n {
		return n - 1
	}
	return cur
}

// activeOrClamp prefers the active window's index when the previous cursor is
// out of range (e.g. after first load), else keeps the cursor in bounds.
func activeOrClamp(cur int, windows []tmux.WindowInfo) int {
	if cur >= 0 && cur < len(windows) {
		return cur
	}
	for i, w := range windows {
		if w.Active {
			return i
		}
	}
	return clamp(cur, len(windows))
}

func activePaneOrClamp(cur int, panes []tmux.PaneInfo) int {
	if cur >= 0 && cur < len(panes) {
		return cur
	}
	for i, p := range panes {
		if p.Active {
			return i
		}
	}
	return clamp(cur, len(panes))
}
