package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jskoll/wyrm/internal/picker"
	"github.com/jskoll/wyrm/internal/tmux"
)

// funcRunner dispatches tmux calls to a function so tests can stub per-command
// output. The first arg (the tmux subcommand) is the usual discriminator.
type funcRunner struct {
	fn func(args ...string) (string, error)
}

func (r funcRunner) Run(args ...string) (string, error) { return r.fn(args...) }

func nopRunner() funcRunner {
	return funcRunner{fn: func(args ...string) (string, error) { return "", nil }}
}

// run executes a command (if non-nil) and returns its message.
func run(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

func key(s string) tea.KeyMsg {
	switch s {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func update(m Model, msg tea.Msg) (Model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

func TestFocusCycling(t *testing.T) {
	m := New(nopRunner(), nil)
	if m.focus != panelProjects {
		t.Fatalf("initial focus = %d, want panelProjects", m.focus)
	}
	m, _ = update(m, key("tab"))
	if m.focus != panelSessions {
		t.Errorf("after tab focus = %d, want panelSessions", m.focus)
	}
	m, _ = update(m, key("shift+tab"))
	if m.focus != panelProjects {
		t.Errorf("after shift+tab focus = %d, want panelProjects", m.focus)
	}
	m, _ = update(m, key("4"))
	if m.focus != panelPanes {
		t.Errorf("after '4' focus = %d, want panelPanes", m.focus)
	}
	// tab wraps from the last panel back to the first.
	m, _ = update(m, key("tab"))
	if m.focus != panelProjects {
		t.Errorf("after wrap focus = %d, want panelProjects", m.focus)
	}
}

func TestSessionsMsgLoadsWindows(t *testing.T) {
	r := funcRunner{fn: func(args ...string) (string, error) {
		if args[0] == "list-windows" {
			return "0|@1|1|layout|code", nil
		}
		return "", nil
	}}
	m := New(r, nil)
	m, cmd := update(m, sessionsMsg{sessions: []picker.Session{{ID: "$1", Name: "alpha", Windows: 1}}})
	if len(m.sessions) != 1 || m.sessionCur != 0 {
		t.Fatalf("sessions not stored: %+v cur=%d", m.sessions, m.sessionCur)
	}
	msg := run(cmd)
	wm, ok := msg.(windowsMsg)
	if !ok {
		t.Fatalf("follow-up msg = %T, want windowsMsg", msg)
	}
	if wm.sessionID != "$1" {
		t.Errorf("windowsMsg.sessionID = %q, want $1", wm.sessionID)
	}
}

func TestWindowsMsgPicksActiveWindow(t *testing.T) {
	m := New(nopRunner(), nil)
	m.sessions = []picker.Session{{ID: "$1", Name: "alpha"}}
	m.sessionCur = 0
	windows := []tmux.WindowInfo{
		{Index: 0, ID: "@1", Active: false, Name: "one"},
		{Index: 1, ID: "@2", Active: true, Name: "two"},
	}
	m, cmd := update(m, windowsMsg{sessionID: "$1", windows: windows})
	if m.windowCur != 1 {
		t.Errorf("windowCur = %d, want 1 (the active window)", m.windowCur)
	}
	// It should follow up by loading the active window's panes.
	pm, ok := run(cmd).(panesMsg)
	if ok && pm.windowID != "@2" {
		t.Errorf("panesMsg.windowID = %q, want @2", pm.windowID)
	}
}

func TestStaleWindowsMsgIgnored(t *testing.T) {
	m := New(nopRunner(), nil)
	m.sessions = []picker.Session{{ID: "$1"}, {ID: "$2"}}
	m.sessionCur = 0 // current session is $1
	before := m.windows
	m, cmd := update(m, windowsMsg{sessionID: "$2", windows: []tmux.WindowInfo{{ID: "@9"}}})
	if len(m.windows) != len(before) {
		t.Errorf("stale windowsMsg for $2 was applied while $1 is current")
	}
	if cmd != nil {
		t.Errorf("stale windowsMsg produced a follow-up command")
	}
}

func TestPreviewMsgSetsContent(t *testing.T) {
	m := New(nopRunner(), nil)
	m.panes = []tmux.PaneInfo{{ID: "%1", Command: "nvim"}}
	m.paneCur = 0
	m, _ = update(m, previewMsg{paneID: "%1", content: "hello world"})
	if m.preview != "hello world" {
		t.Errorf("preview = %q, want %q", m.preview, "hello world")
	}
	if !strings.Contains(m.previewTitle, "nvim") {
		t.Errorf("previewTitle = %q, want it to mention the command", m.previewTitle)
	}
}

func TestNavigationResetsChildCursors(t *testing.T) {
	r := funcRunner{fn: func(args ...string) (string, error) { return "", nil }}
	m := New(r, nil)
	m.focus = panelSessions
	m.sessions = []picker.Session{{ID: "$1"}, {ID: "$2"}}
	m.sessionCur = 0
	m.windowCur = 3
	m.paneCur = 2
	m, cmd := update(m, key("down")) // move to $2
	if m.sessionCur != 1 {
		t.Fatalf("sessionCur = %d, want 1", m.sessionCur)
	}
	if m.windowCur != -1 || m.paneCur != -1 {
		t.Errorf("child cursors not reset: windowCur=%d paneCur=%d", m.windowCur, m.paneCur)
	}
	if run(cmd) == nil {
		t.Errorf("moving session should trigger a window reload")
	}
}

func TestEnterSetsPendingAttachAndQuits(t *testing.T) {
	m := New(nopRunner(), nil)
	m.focus = panelSessions
	m.sessions = []picker.Session{{ID: "$7", Name: "target"}}
	m.sessionCur = 0
	m, cmd := update(m, key("enter"))
	if m.pendingAttach != "$7" {
		t.Errorf("pendingAttach = %q, want $7", m.pendingAttach)
	}
	if _, ok := run(cmd).(tea.QuitMsg); !ok {
		t.Errorf("enter did not return tea.Quit")
	}
}

func TestSelfPanePreviewSuppressed(t *testing.T) {
	m := New(nopRunner(), nil)
	m.selfPane = "%1"
	m.panes = []tmux.PaneInfo{{ID: "%1", Command: "wyrm"}}
	m.paneCur = 0
	cmd := m.reloadPreview()
	if cmd != nil {
		t.Error("reloadPreview should not capture the pane wyrm runs in")
	}
	if m.preview != selfPreviewNotice {
		t.Errorf("preview = %q, want the self-pane notice", m.preview)
	}
	// A different pane still gets a real capture command.
	m.panes = []tmux.PaneInfo{{ID: "%2", Command: "nvim"}}
	if cmd := m.reloadPreview(); cmd == nil {
		t.Error("reloadPreview should capture a pane other than wyrm's own")
	}
}

func TestQuitKeys(t *testing.T) {
	m := New(nopRunner(), nil)
	_, cmd := update(m, key("q"))
	if _, ok := run(cmd).(tea.QuitMsg); !ok {
		t.Errorf("q did not quit")
	}
}
