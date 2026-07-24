// Package tui implements wyrm's interactive, full-screen session manager
// (wyrm -tui): a lazygit-style multi-panel view over the running tmux world.
//
// Phase 1 is a read-only navigator: three stacked panels on the left
// (Sessions -> Windows -> Panes) drive a live pane-content preview on the
// right, and Enter attaches to the selected session. Management actions
// (kill/rename/new-window) and a Projects panel over .wyrm.toml configs land in
// later phases.
//
// The model follows the repo convention of taking a tmux.Runner rather than
// shelling out directly, so Update stays pure and unit-testable: every tmux
// call happens inside a tea.Cmd closure that captures the Runner, and Update
// only ever reacts to the resulting messages.
package tui

import (
	"io"
	"os"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jskoll/wyrm/internal/config"
	"github.com/jskoll/wyrm/internal/picker"
	"github.com/jskoll/wyrm/internal/tmux"
)

// refreshInterval is how often the selected pane's preview is re-captured so
// the view stays live.
const refreshInterval = time.Second

// selfPreviewNotice replaces the live capture when the selected pane is the one
// wyrm -tui is itself running in: capturing it would render the TUI back into
// its own preview, a mirror-of-a-mirror that thrashes on every refresh.
const selfPreviewNotice = "This pane is running wyrm -tui.\nPreview hidden to avoid a feedback loop."

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
// destructive kills), or a text-entry prompt (rename / new-window).
type mode int

const (
	modeNormal mode = iota
	modeConfirm
	modePrompt
	modeHelp
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

	// selfPane is the tmux pane ID wyrm -tui is running in ($TMUX_PANE), or ""
	// when launched outside tmux. Its preview is suppressed to avoid a mirror
	// loop.
	selfPane string

	// modal state.
	mode          mode
	pending       pendingAction
	confirmPrompt string          // shown in modeConfirm
	promptTitle   string          // label shown in modePrompt
	textInput     textinput.Model // active in modePrompt
	layoutIdx     int             // rotates through cycleLayouts on "L"

	err error

	// pendingAttach is the tmux session ID (e.g. "$3") to hand the terminal to
	// once the program exits. The alt-screen program can't attach in-place, so
	// runTUI performs the attach after Run returns — mirroring runPicker.
	pendingAttach string
}

// New builds a Model backed by runner. windowCur/paneCur start at -1 so the
// first load of each snaps to the active window/pane rather than index 0.
// settings may be nil (the Projects panel then lists only local configs).
func New(runner tmux.Runner, settings *config.Settings) Model {
	return Model{runner: runner, settings: settings, windowCur: -1, paneCur: -1, selfPane: os.Getenv("TMUX_PANE")}
}

// Init loads the initial project and session lists and starts the refresh
// ticker.
func (m Model) Init() tea.Cmd {
	return tea.Batch(loadProjects(m.runner, m.settings), loadSessions(m.runner), tick())
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

// --- selection accessors ---

func (m Model) currentProject() (Project, bool) {
	if m.projectCur < 0 || m.projectCur >= len(m.projects) {
		return Project{}, false
	}
	return m.projects[m.projectCur], true
}

func (m Model) currentSession() (picker.Session, bool) {
	if m.sessionCur < 0 || m.sessionCur >= len(m.sessions) {
		return picker.Session{}, false
	}
	return m.sessions[m.sessionCur], true
}

func (m Model) currentWindow() (tmux.WindowInfo, bool) {
	if m.windowCur < 0 || m.windowCur >= len(m.windows) {
		return tmux.WindowInfo{}, false
	}
	return m.windows[m.windowCur], true
}

func (m Model) currentPane() (tmux.PaneInfo, bool) {
	if m.paneCur < 0 || m.paneCur >= len(m.panes) {
		return tmux.PaneInfo{}, false
	}
	return m.panes[m.paneCur], true
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
		// Refresh the live pane preview, then reschedule. A config preview
		// (Projects panel focused) is static, so leave it alone.
		var cmd tea.Cmd
		if m.focus != panelProjects {
			cmd = m.reloadPreview()
		}
		return m, tea.Batch(cmd, tick())

	case projectsMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.projects = msg.projects
		m.projectCur = clamp(m.projectCur, len(m.projects))
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
		m.sessionCur = clamp(m.sessionCur, len(m.sessions))
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
		m.windowCur = activeOrClamp(m.windowCur, m.windows)
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
		m.paneCur = activePaneOrClamp(m.paneCur, m.panes)
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
		// Any key dismisses the help overlay.
		m.mode = modeNormal
		return m, nil
	}
	return m.handleNormalKey(msg)
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
		return m.startNewWindow()
	case "L":
		return m.cycleLayout()
	case "z":
		return m.zoomPane()
	case "e":
		return m.editProject()
	case "?":
		m.mode = modeHelp
		return m, nil
	}
	return m, nil
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
	case "y", "Y", "enter":
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
	switch m.focus {
	case panelProjects:
		next := m.projectCur + delta
		if next < 0 || next >= len(m.projects) {
			return m, nil
		}
		m.projectCur = next
		return m, m.updatePreview()
	case panelSessions:
		next := m.sessionCur + delta
		if next < 0 || next >= len(m.sessions) {
			return m, nil
		}
		m.sessionCur = next
		// Parent changed: snap the child selections to the new session's
		// active window/pane on reload.
		m.windowCur, m.paneCur = -1, -1
		return m, m.reloadWindows()
	case panelWindows:
		next := m.windowCur + delta
		if next < 0 || next >= len(m.windows) {
			return m, nil
		}
		m.windowCur = next
		m.paneCur = -1
		return m, m.reloadPanes()
	case panelPanes:
		next := m.paneCur + delta
		if next < 0 || next >= len(m.panes) {
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
	// Never capture the pane wyrm -tui itself occupies — that mirrors the TUI
	// into its own preview and thrashes on every refresh.
	if m.selfPane != "" && p.ID == m.selfPane {
		m.previewTitle = p.ID + " " + p.Command
		m.preview = selfPreviewNotice
		return nil
	}
	return loadPreview(m.runner, p.ID)
}

// Run drives the program to completion and returns the tmux session ID to
// attach to (empty if the user quit without choosing). stderr is reserved for
// future diagnostics and to keep the signature parallel to picker.Run.
func Run(runner tmux.Runner, settings *config.Settings, stderr io.Writer) (pendingAttach string, err error) {
	_ = stderr
	fm, err := tea.NewProgram(New(runner, settings), tea.WithAltScreen()).Run()
	if err != nil {
		return "", err
	}
	return fm.(Model).pendingAttach, nil
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
