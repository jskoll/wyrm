package tui

import (
	"errors"
	"strings"
	"testing"
)

// The session-level destructive actions had no coverage anywhere, while every
// window- and pane-level sibling was exercised by the integration tests. They
// are the highest-blast-radius operations in the product: a bug in either
// destroys running work.

func TestKillSessionCmdKillsAndRelists(t *testing.T) {
	var calls []string
	r := funcRunner{fn: func(args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "list-sessions" {
			return "$2|1|0|1000|beta", nil
		}
		return "", nil
	}}

	msg := killSessionCmd(r, "$1")()
	sm, ok := msg.(sessionsMsg)
	if !ok {
		t.Fatalf("killSessionCmd produced %T, want sessionsMsg", msg)
	}
	if sm.err != nil {
		t.Fatalf("sessionsMsg.err = %v", sm.err)
	}
	if len(sm.sessions) != 1 || sm.sessions[0].Name != "beta" {
		t.Errorf("sessions = %+v, want the refreshed list", sm.sessions)
	}
	if !contains(calls, "kill-session -t $1") {
		t.Errorf("kill-session not issued: %v", calls)
	}
}

func TestKillSessionCmdReportsFailure(t *testing.T) {
	r := funcRunner{fn: func(args ...string) (string, error) {
		if args[0] == "kill-session" {
			return "no such session", errors.New("exit status 1")
		}
		return "", nil
	}}
	msg := killSessionCmd(r, "$1")()
	if _, ok := msg.(actionErrMsg); !ok {
		t.Fatalf("killSessionCmd on failure produced %T, want actionErrMsg", msg)
	}
}

func TestRenameSessionCmdRenamesAndRelists(t *testing.T) {
	var calls []string
	r := funcRunner{fn: func(args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "list-sessions" {
			return "$1|1|0|1000|renamed", nil
		}
		return "", nil
	}}

	msg := renameSessionCmd(r, "$1", "renamed")()
	sm, ok := msg.(sessionsMsg)
	if !ok {
		t.Fatalf("renameSessionCmd produced %T, want sessionsMsg", msg)
	}
	if sm.err != nil {
		t.Fatalf("sessionsMsg.err = %v", sm.err)
	}
	// Targeted by ID, never by the old name: a name containing "." would be
	// misparsed by tmux's -t syntax.
	if !contains(calls, "rename-session -t $1 renamed") {
		t.Errorf("rename-session not issued by ID: %v", calls)
	}
}

func TestRenameSessionCmdReportsFailure(t *testing.T) {
	r := funcRunner{fn: func(args ...string) (string, error) {
		if args[0] == "rename-session" {
			return "bad name", errors.New("exit status 1")
		}
		return "", nil
	}}
	msg := renameSessionCmd(r, "$1", "nope")()
	if _, ok := msg.(actionErrMsg); !ok {
		t.Fatalf("renameSessionCmd on failure produced %T, want actionErrMsg", msg)
	}
}

func TestZoomPaneCmdReportsFailure(t *testing.T) {
	r := funcRunner{fn: func(...string) (string, error) {
		return "no such pane", errors.New("exit status 1")
	}}
	if _, ok := zoomPaneCmd(r, "%1")().(actionErrMsg); !ok {
		t.Error("zoomPaneCmd on failure should produce actionErrMsg")
	}
}

// TestConfirmRequiresY: enter is "attach" in normal mode, so accepting it as a
// confirmation turned a reflexive x-then-enter into a killed session.
func TestConfirmRequiresY(t *testing.T) {
	m := modelWithData(nopRunner())
	m.focus = panelSessions
	m, _ = update(m, key("x"))
	if m.mode != modeConfirm {
		t.Fatalf("mode = %v, want modeConfirm after x", m.mode)
	}
	after, cmd := update(m, key("enter"))
	if cmd != nil {
		t.Error("enter must not confirm a kill")
	}
	if after.mode != modeConfirm {
		t.Errorf("mode = %v, want the confirm still open after enter", after.mode)
	}
	confirmed, cmd := update(m, key("y"))
	if cmd == nil {
		t.Error("y should confirm the kill")
	}
	if confirmed.mode != modeNormal {
		t.Errorf("mode = %v, want modeNormal after y", confirmed.mode)
	}
}

// TestActionErrorIsRendered: errors used to be shown only when the preview
// happened to be empty — which in normal use it never is — so a failed action
// was indistinguishable from a successful one.
func TestActionErrorIsRendered(t *testing.T) {
	m := modelWithData(nopRunner())
	m.width, m.height = 100, 30
	m.ready = true
	m.preview = "some pane output"
	m, _ = update(m, actionErrMsg{err: errors.New("kill-session failed")})

	view := m.View()
	if !strings.Contains(view, "kill-session failed") {
		t.Errorf("view does not report the failed action:\n%s", view)
	}

	// And any key dismisses it.
	m, _ = update(m, key("j"))
	if m.err != nil {
		t.Errorf("err = %v, want it cleared by the next keypress", m.err)
	}
}

// TestFocusScopedKeys: n/z/L used to fire from any panel, acting on a
// selection in a panel that didn't have focus while the contextual footer said
// otherwise.
func TestFocusScopedKeys(t *testing.T) {
	m := modelWithData(nopRunner())
	m.focus = panelProjects
	if after, cmd := update(m, key("n")); cmd != nil || after.mode == modePrompt {
		t.Error("n on the Projects panel must not open a new-window prompt")
	}
	if _, cmd := update(m, key("L")); cmd != nil {
		t.Error("L on the Projects panel must not cycle a layout")
	}
	if _, cmd := update(m, key("z")); cmd != nil {
		t.Error("z on the Projects panel must not zoom a pane")
	}
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
