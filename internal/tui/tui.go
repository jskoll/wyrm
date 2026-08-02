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

	"github.com/jskoll/wyrm/internal/agent"
	"github.com/jskoll/wyrm/internal/config"
	"github.com/jskoll/wyrm/internal/sessions"
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

	// noPanel is "no such panel", used by childOf to end the cascade.
	noPanel panel = -1
)

// previewSource tracks what the main panel is currently showing, so the ticker
// only refreshes a live pane capture (not a static config preview).
type previewSource int

const (
	previewPane previewSource = iota
	previewConfig
)

// clickState remembers the previous mouse press, which is the whole of what a
// double click needs to recognise itself: same row, same panel, recently
// enough. counted is 0 or 1 — the click that would make it 2 is dispatched as
// an activation instead of being counted.
type clickState struct {
	counted int
	at      time.Time
	panel   panel
	row     int
}

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
	// modeMenu is the right-click context menu. It's a mode rather than an
	// overlay flag because it captures the keys it shares with normal mode
	// (j/k/enter) while it's open.
	modeMenu
)

// Model is the Bubble Tea model for the TUI. It is a plain value type; Update
// returns an updated copy. tmux access is confined to the command closures, so
// the model itself holds only data and view state.
type Model struct {
	runner   tmux.Runner
	settings *config.Settings

	focus panel

	projects []Project
	sessions []sessions.Session
	windows  []tmux.WindowInfo
	panes    []tmux.PaneInfo

	// cur is each panel's selected row, indexed by panel.
	//
	// The four lists above stay separately typed — they hold genuinely
	// different things — but the cursors are four ints with identical
	// semantics, and holding them apart meant every question about "the
	// selection" became a switch. Two of those switches (cursorFor and
	// focusedCursor) were the same switch written twice.
	//
	// -1 means "not chosen yet": the first load of a panel then snaps to
	// whatever tmux says is active rather than to row 0.
	cur [numPanels]int

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

	// agents holds the last agent-pane scan: which sessions, windows, and panes
	// hold an AI agent that's waiting on the user. Empty until the first scan.
	agents agentStatus

	// agentProfiles is the compiled detector configuration, resolved once at
	// startup rather than per scan — it involves compiling regexps, and the scan
	// runs on a timer.
	agentProfiles []agent.Profile

	// blurred is set while the terminal reports the window as unfocused. The
	// refresh tickers keep running but skip their work — see the tickMsg case.
	blurred bool

	// compact drops the Projects and Panes panels, leaving Sessions over
	// Windows. It is what `wyrm pick` runs — see RunPicker. Focus and the mouse
	// hit test both walk panels(), so nothing has to special-case it beyond the
	// panel list itself.
	compact bool

	// mouseOn mirrors the terminal's mouse-reporting state, toggled by "m".
	// Capturing the mouse takes click-drag text selection away from the
	// terminal, so it has to be surrenderable without restarting the TUI.
	mouseOn bool

	// lastClick is what a double click is measured against — see clickState.
	lastClick clickState

	// clock reads the current time, so tests can drive the double-click window
	// without sleeping. nil means time.Now — see Model.now.
	clock func() time.Time

	// The context menu (modeMenu). menuX/menuY are the click point it's
	// anchored to, not its top-left corner — see menuBox.
	menu         []menuEntry
	menuCur      int
	menuX, menuY int

	err error

	// pendingAttach is the tmux session ID (e.g. "$3") to hand the terminal to
	// once the program exits. The alt-screen program can't attach in-place, so
	// runTUI performs the attach after Run returns — mirroring runPicker.
	pendingAttach string
}

// New builds a Model backed by runner. The Windows and Panes cursors start at
// -1 so the first load of each snaps to the active window/pane rather than
// index 0.
// Focus starts on Sessions rather than the first panel: the common reason to
// open the TUI is to reach a running session, and it makes the initial preview
// a live pane capture instead of a config file.
// settings may be nil (the Projects panel then lists only local configs).
func New(runner tmux.Runner, settings *config.Settings) Model {
	// A broken profile is reported by Run before the alt screen opens; New
	// itself falls back to the built-in one so the zero-config and test paths
	// stay total.
	profiles, err := agentProfiles(settings)
	if err != nil {
		profiles = []agent.Profile{agent.DefaultProfile()}
	}
	return Model{
		runner:        runner,
		settings:      settings,
		focus:         panelSessions,
		cur:           [numPanels]int{panelWindows: -1, panelPanes: -1},
		selfPane:      os.Getenv("TMUX_PANE"),
		mouseOn:       settings.MouseEnabled(),
		agentProfiles: profiles,
	}
}

// Init loads the initial project and session lists and starts the refresh
// ticker.
func (m Model) Init() tea.Cmd {
	return tea.Batch(append(m.refreshLists(), tick(), listTick())...)
}

// refreshLists re-reads everything the left column shows. Four places want
// exactly this — startup, the "R" key, regaining terminal focus, and the list
// ticker — and they were four copies of the same command set.
//
// It returns the commands rather than a tea.Batch of them so callers can splice
// in their own without nesting one batch inside another.
func (m Model) refreshLists() []tea.Cmd {
	return []tea.Cmd{loadProjects(m.runner, m.settings), loadSessions(m.runner), m.agentCmd()}
}

// --- messages ---

type sessionsMsg struct {
	sessions []sessions.Session
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
		s, err := sessions.List(r)
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
		if _, ok := sessions.FuzzyMatch(f, p.Name); ok {
			out = append(out, p)
		}
	}
	return out
}

func (m Model) visibleSessions() []sessions.Session {
	f := m.filterFor(panelSessions)
	if f == "" {
		return m.sessions
	}
	out := make([]sessions.Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		if _, ok := sessions.FuzzyMatch(f, s.Name); ok {
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
		if _, ok := sessions.FuzzyMatch(f, w.Name); ok {
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
		if _, ok := sessions.FuzzyMatch(f, p.ID+" "+p.Command); ok {
			out = append(out, p)
		}
	}
	return out
}

// compactPanels is the panel set `wyrm pick` shows: find a session, optionally
// pick the window to land on. Projects and Panes are browsing tools, which is
// what the full TUI is for.
var compactPanels = []panel{panelSessions, panelWindows}

// allPanels is the full cascade, in render order.
var allPanels = []panel{panelProjects, panelSessions, panelWindows, panelPanes}

// panels returns the panels this model shows, in top-to-bottom order. Focus
// cycling, the layout, and the mouse hit test all read it, so "which panels
// exist" is stated once.
func (m Model) panels() []panel {
	if m.compact {
		return compactPanels
	}
	return allPanels
}

// panelIndex is p's position in panels(), or -1 when this model doesn't show it.
func (m Model) panelIndex(p panel) int {
	for i, q := range m.panels() {
		if q == p {
			return i
		}
	}
	return -1
}

// --- selection accessors ---

func (m Model) currentProject() (Project, bool) {
	list := m.visibleProjects()
	if m.cur[panelProjects] < 0 || m.cur[panelProjects] >= len(list) {
		return Project{}, false
	}
	return list[m.cur[panelProjects]], true
}

func (m Model) currentSession() (sessions.Session, bool) {
	list := m.visibleSessions()
	if m.cur[panelSessions] < 0 || m.cur[panelSessions] >= len(list) {
		return sessions.Session{}, false
	}
	return list[m.cur[panelSessions]], true
}

func (m Model) currentWindow() (tmux.WindowInfo, bool) {
	list := m.visibleWindows()
	if m.cur[panelWindows] < 0 || m.cur[panelWindows] >= len(list) {
		return tmux.WindowInfo{}, false
	}
	return list[m.cur[panelWindows]], true
}

func (m Model) currentPane() (tmux.PaneInfo, bool) {
	list := m.visiblePanes()
	if m.cur[panelPanes] < 0 || m.cur[panelPanes] >= len(list) {
		return tmux.PaneInfo{}, false
	}
	return list[m.cur[panelPanes]], true
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

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case agentStatusMsg:
		// A failed scan keeps the previous markers rather than clearing them or
		// claiming the footer: this is an optional decoration polled on a timer,
		// and one unlucky tmux call shouldn't wipe an error the user is reading.
		if msg.err == nil {
			m.agents = msg.status
		}
		return m, nil

	case tea.FocusMsg:
		// Terminal focus came back: refresh immediately rather than making the
		// user wait out the rest of a tick for a stale screen.
		m.blurred = false
		return m, tea.Batch(append(m.refreshLists(), m.reloadPreview())...)

	case tea.BlurMsg:
		m.blurred = true
		return m, nil

	case tickMsg:
		// Refresh the live pane preview, then reschedule. A config preview is
		// static, so leave it alone — keyed off what's actually being shown
		// rather than off which panel has focus.
		//
		// The ticker keeps running while the terminal is unfocused but does no
		// work: a session manager sitting in a background tab was spending a
		// capture-pane every second and a list sweep every three, forever, for a
		// screen nobody was looking at.
		var cmd tea.Cmd
		if !m.blurred && m.previewSrc == previewPane {
			cmd = m.reloadPreview()
		}
		return m, tea.Batch(cmd, tick())

	case listTickMsg:
		// Re-read the lists so sessions started or ended elsewhere show up.
		// Held off while a modal is open, so the data under a confirm prompt
		// can't change between reading it and answering it.
		if m.blurred || (m.mode != modeNormal && m.mode != modeFilter) {
			return m, listTick()
		}
		return m, tea.Batch(append(m.refreshLists(), listTick())...)

	case projectsMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.projects = msg.projects
		m.cur[panelProjects] = clamp(m.cur[panelProjects], m.panelLen(panelProjects))
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
		m.cur[panelSessions] = clamp(m.cur[panelSessions], m.panelLen(panelSessions))
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
		m.cur[panelWindows] = activeOrClamp(m.cur[panelWindows], m.visibleWindows())
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
		m.cur[panelPanes] = activePaneOrClamp(m.cur[panelPanes], m.visiblePanes())
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
	case modeMenu:
		return m.handleMenuKey(msg)
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
		// In the compact picker the filter *is* the interaction — it opens
		// focused and typing is the first thing you do — so Enter finishes the
		// job rather than just the filter. Requiring a second Enter to attach
		// would be a step backwards from the fzf-style chooser this replaced.
		// The full TUI keeps the lazygit behavior: there `/` is one tool among
		// several and Enter should commit the search, not act on it.
		if m.compact {
			return m.activateSelection()
		}
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
	m.cur[m.focus] = clamp(m.cur[m.focus], m.panelLen(m.focus))
	return m, m.selectionChanged(m.focus)
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
		return m, tea.Batch(m.refreshLists()...)
	case "m":
		// Hand the mouse back to the terminal (and take it again). While it's
		// captured, click-drag selects nothing — this is the way out for anyone
		// who wants to copy text off the screen.
		m.mouseOn = !m.mouseOn
		return m, m.mouseCmd()
	case "M":
		// The keyboard route to the right-click menu — see openMenuAtSelection
		// for why the mouse can't be the only way in.
		return m.openMenuAtSelection()
	case "tab", "l", "right":
		return m.cycleFocus(1)
	case "shift+tab", "h", "left":
		return m.cycleFocus(-1)
	case "1", "2", "3", "4":
		// The digits address the panels this model actually shows, so in compact
		// mode "1" is Sessions rather than a Projects panel that isn't there.
		return m.focusPanelAt(int(msg.String()[0] - '1'))
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

// cycleFocus moves focus by delta positions through the shown panels, wrapping.
func (m Model) cycleFocus(delta int) (tea.Model, tea.Cmd) {
	list := m.panels()
	i := m.panelIndex(m.focus)
	if i < 0 {
		i = 0
	}
	m.focus = list[((i+delta)%len(list)+len(list))%len(list)]
	return m, m.updatePreview()
}

// focusPanelAt focuses the i'th shown panel, ignoring an out-of-range index so
// a "4" in compact mode does nothing rather than something surprising.
func (m Model) focusPanelAt(i int) (tea.Model, tea.Cmd) {
	list := m.panels()
	if i < 0 || i >= len(list) {
		return m, nil
	}
	m.focus = list[i]
	return m, m.updatePreview()
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
// focused panel. What that is — the action and the wording — comes from the
// panel table; this only puts the modal up.
func (m Model) startKill() (tea.Model, tea.Cmd) {
	f := m.focus.spec().kill
	if f == nil {
		return m, nil
	}
	pending, prompt, ok := f(m)
	if !ok {
		return m, nil
	}
	m.pending, m.confirmPrompt = pending, prompt
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
	return m, startProjectCmd(m.runner, p)
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
	return m.moveCursorTo(m.cur[m.focus] + delta)
}

// moveCursorTo sets the focused panel's selection, clamping to the ends rather
// than refusing to move — a PgDn near the bottom should land on the last row,
// not do nothing — and reloads the panels below it.
func (m Model) moveCursorTo(next int) (tea.Model, tea.Cmd) {
	return m.setCursor(m.focus, next)
}

// setCursor is moveCursorTo for an arbitrary panel. The mouse needs it: a click
// or a wheel event names the panel it landed on, rather than acting on whatever
// currently has focus.
func (m Model) setCursor(p panel, next int) (tea.Model, tea.Cmd) {
	n := m.panelLen(p)
	if n == 0 {
		return m, nil
	}
	next = clampTo(next, n)
	if next == m.cur[p] {
		return m, nil
	}
	m.cur[p] = next
	return m, m.selectionChanged(p)
}

// selectionChanged resets the panels that hang off p and returns the command
// that reloads them.
//
// The cascade used to be spelled out per panel in both setCursor and
// clampFocused, four cases each. Resetting a child to -1 rather than 0 is what
// makes the reload snap to whatever tmux reports as active instead of to the
// first row. Which panel feeds which, and what to re-fetch, come from the panel
// table — see panels.go.
func (m *Model) selectionChanged(p panel) tea.Cmd {
	for child := p.spec().child; child != noPanel; child = child.spec().child {
		m.cur[child] = -1
	}
	if f := p.spec().reload; f != nil {
		return f(m)
	}
	return nil
}

// clampTo keeps an index inside [0, n).
func clampTo(i, n int) int {
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
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

// Run drives the full four-panel session manager to completion and returns the
// tmux session ID to attach to (empty if the user quit without choosing). An
// error the model was still holding when the user quit is reported on stderr —
// the alt screen is gone by then, so the footer that would have shown it is too.
func Run(runner tmux.Runner, settings *config.Settings, stderr io.Writer) (pendingAttach string, err error) {
	return runProgram(New(runner, settings), settings, stderr)
}

// RunPicker drives the compact, fzf-style chooser behind `wyrm pick`: the same
// model and the same key map, with only the Sessions and Windows panels shown
// and the filter already open so typing narrows the list immediately.
//
// It is the same program as Run on purpose. `wyrm pick` used to be a second,
// independent implementation — a hand-rolled raw-mode terminal loop with its
// own escape-sequence decoder, its own frame renderer, its own signal handling
// and its own key names — offering a strict subset of what the TUI already did.
// Two key maps that disagreed about how to kill a session was the visible cost;
// the invisible one was that every new feature had to be built twice or quietly
// exist in only one of them.
func RunPicker(runner tmux.Runner, settings *config.Settings, stderr io.Writer) (pendingAttach string, err error) {
	m := New(runner, settings)
	m.compact = true
	m.focus = panelSessions
	// Straight into filter mode: `pick` exists to be typed at. The full TUI
	// starts in normal mode because it is a browser first and a finder second.
	m.mode, m.filtering = modeFilter, true
	return runProgram(m, settings, stderr)
}

func runProgram(m Model, settings *config.Settings, stderr io.Writer) (pendingAttach string, err error) {
	// Before the alt screen: a broken theme file or agent profile has to be
	// reportable, and the alt screen would wipe the message on its way up.
	theme, err := LoadTheme()
	if err != nil {
		return "", err
	}
	SetTheme(theme)
	if _, err := agentProfiles(settings); err != nil {
		return "", err
	}

	// ReportFocus so the refresh tickers can idle while the terminal is in a
	// background tab — see the tickMsg case in Update.
	opts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithReportFocus()}
	// Cell motion, not all motion: the menu wants hover tracking while a button
	// is down, but reporting every idle pointer move would wake the program on
	// each pixel of travel for no benefit.
	if settings.MouseEnabled() {
		opts = append(opts, tea.WithMouseCellMotion())
	}

	fm, err := tea.NewProgram(m, opts...).Run()
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
