package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jskoll/wyrm/internal/picker"
	"github.com/jskoll/wyrm/internal/tmux"
)

// actionOp identifies a pending management action awaiting confirmation (kills)
// or text input (rename/new-window). Storing the op + its target IDs as plain
// data — rather than a closure — keeps Model comparable and easy to unit-test.
type actionOp int

const (
	opNone actionOp = iota
	opKillSession
	opKillWindow
	opKillPane
	opRenameSession
	opRenameWindow
	opNewWindow
	opKillProject
)

// pendingAction captures what a modal will do once confirmed/submitted, along
// with the tmux IDs it needs. The relevant subset of IDs is filled per op.
type pendingAction struct {
	op        actionOp
	sessionID string
	windowID  string
	paneID    string
	path      string // config path, for opKillProject
}

// actionErrMsg reports that a management action failed; Update stores it in
// m.err for display. Successful actions instead return a reload message
// (sessionsMsg/windowsMsg/panesMsg) so the existing handlers refresh the view.
type actionErrMsg struct{ err error }

// tmux layouts cycled by the "L" key on the Windows panel, in tmux's own order.
var cycleLayouts = []string{"even-horizontal", "even-vertical", "main-horizontal", "main-vertical", "tiled"}

// --- action commands: each mutates via tmux, then re-lists the affected level
// and returns the matching load message so Update folds the fresh data in. ---

func killSessionCmd(r tmux.Runner, sessionID string) tea.Cmd {
	return func() tea.Msg {
		if err := picker.KillSession(r, sessionID); err != nil {
			return actionErrMsg{err}
		}
		s, err := picker.ListSessions(r)
		return sessionsMsg{sessions: s, err: err}
	}
}

func killWindowCmd(r tmux.Runner, sessionID, windowID string) tea.Cmd {
	return func() tea.Msg {
		if err := tmux.KillWindow(r, windowID); err != nil {
			return actionErrMsg{err}
		}
		w, err := tmux.ListWindows(r, sessionID)
		return windowsMsg{sessionID: sessionID, windows: w, err: err}
	}
}

func killPaneCmd(r tmux.Runner, windowID, paneID string) tea.Cmd {
	return func() tea.Msg {
		if err := tmux.KillPane(r, paneID); err != nil {
			return actionErrMsg{err}
		}
		p, err := tmux.ListPanes(r, windowID)
		return panesMsg{windowID: windowID, panes: p, err: err}
	}
}

func renameSessionCmd(r tmux.Runner, sessionID, name string) tea.Cmd {
	return func() tea.Msg {
		if err := tmux.RenameSession(r, sessionID, name); err != nil {
			return actionErrMsg{err}
		}
		s, err := picker.ListSessions(r)
		return sessionsMsg{sessions: s, err: err}
	}
}

func renameWindowCmd(r tmux.Runner, sessionID, windowID, name string) tea.Cmd {
	return func() tea.Msg {
		if err := tmux.RenameWindow(r, windowID, name); err != nil {
			return actionErrMsg{err}
		}
		w, err := tmux.ListWindows(r, sessionID)
		return windowsMsg{sessionID: sessionID, windows: w, err: err}
	}
}

func newWindowCmd(r tmux.Runner, sessionID, name string) tea.Cmd {
	return func() tea.Msg {
		if _, _, err := tmux.NewWindow(r, sessionID, name, ""); err != nil {
			return actionErrMsg{err}
		}
		w, err := tmux.ListWindows(r, sessionID)
		return windowsMsg{sessionID: sessionID, windows: w, err: err}
	}
}

func selectLayoutCmd(r tmux.Runner, windowID, layout string) tea.Cmd {
	return func() tea.Msg {
		if err := tmux.SelectLayout(r, windowID, layout); err != nil {
			return actionErrMsg{err}
		}
		p, err := tmux.ListPanes(r, windowID)
		return panesMsg{windowID: windowID, panes: p, err: err}
	}
}

func zoomPaneCmd(r tmux.Runner, paneID string) tea.Cmd {
	return func() tea.Msg {
		if err := tmux.ZoomPane(r, paneID); err != nil {
			return actionErrMsg{err}
		}
		return nil
	}
}

// selectTargetCmd pre-selects the window (and pane) an attach should land on,
// then resolves to nil so it can be sequenced before tea.Quit.
func selectTargetCmd(r tmux.Runner, windowID, paneID string) tea.Cmd {
	return func() tea.Msg {
		_ = tmux.SelectWindow(r, windowID)
		if paneID != "" {
			_ = tmux.SelectPane(r, paneID)
		}
		return nil
	}
}

// executePending builds the command for a confirmed kill action.
func (m Model) executePending() tea.Cmd {
	switch m.pending.op {
	case opKillSession:
		return killSessionCmd(m.runner, m.pending.sessionID)
	case opKillWindow:
		return killWindowCmd(m.runner, m.pending.sessionID, m.pending.windowID)
	case opKillPane:
		return killPaneCmd(m.runner, m.pending.windowID, m.pending.paneID)
	case opKillProject:
		return killProjectCmd(m.runner, m.settings, m.pending.path)
	}
	return nil
}

// executePendingWithValue builds the command for a submitted text action.
func (m Model) executePendingWithValue(value string) tea.Cmd {
	switch m.pending.op {
	case opRenameSession:
		return renameSessionCmd(m.runner, m.pending.sessionID, value)
	case opRenameWindow:
		return renameWindowCmd(m.runner, m.pending.sessionID, m.pending.windowID, value)
	case opNewWindow:
		return newWindowCmd(m.runner, m.pending.sessionID, value)
	}
	return nil
}
