package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jskoll/wyrm/internal/sessions"
	"github.com/jskoll/wyrm/internal/tmux"
)

// modelWithData returns a model wired to a recording runner with one session,
// one window, and one pane selected.
func modelWithData(r tmux.Runner) Model {
	m := New(r, nil)
	m.sessions = []sessions.Session{{ID: "$1", Name: "webapp"}}
	m.windows = []tmux.WindowInfo{{Index: 0, ID: "@1", Name: "code"}}
	m.panes = []tmux.PaneInfo{{ID: "%1", Index: 0, Command: "nvim"}}
	m.cur[panelSessions], m.cur[panelWindows], m.cur[panelPanes] = 0, 0, 0
	return m
}

func TestKillWindowConfirmFlow(t *testing.T) {
	var calls []string
	r := funcRunner{fn: func(args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		return "", nil
	}}
	m := modelWithData(r)
	m.focus = panelWindows

	// x opens a confirm modal; nothing executed yet.
	m, cmd := update(m, key("x"))
	if m.mode != modeConfirm {
		t.Fatalf("mode = %d, want modeConfirm", m.mode)
	}
	if !strings.Contains(m.confirmPrompt, "code") {
		t.Errorf("confirmPrompt = %q, want it to name the window", m.confirmPrompt)
	}
	if cmd != nil {
		t.Error("opening the modal should not run a command yet")
	}

	// 'n' cancels without executing.
	cancel, ccmd := update(m, key("n"))
	if cancel.mode != modeNormal {
		t.Error("'n' should close the modal")
	}
	run(ccmd)
	if len(calls) != 0 {
		t.Errorf("cancel should issue no tmux calls, got %v", calls)
	}

	// 'y' confirms and issues kill-window, then re-lists windows.
	m, cmd = update(m, key("y"))
	if m.mode != modeNormal {
		t.Error("'y' should close the modal")
	}
	msg := run(cmd)
	if _, ok := msg.(windowsMsg); !ok {
		t.Fatalf("confirm produced %T, want windowsMsg", msg)
	}
	found := false
	for _, c := range calls {
		if c == "kill-window -t @1" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a kill-window -t @1 call, got %v", calls)
	}
}

func TestKillPaneConfirmIssuesKill(t *testing.T) {
	var calls []string
	r := funcRunner{fn: func(args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		return "", nil
	}}
	m := modelWithData(r)
	m.focus = panelPanes

	m, _ = update(m, key("x"))
	m, cmd := update(m, key("y"))
	run(cmd) // execute the kill+relist command
	found := false
	for _, c := range calls {
		if c == "kill-pane -t %1" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a kill-pane -t %%1 call, got %v", calls)
	}
}

func TestRenameWindowPromptFlow(t *testing.T) {
	var calls []string
	r := funcRunner{fn: func(args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		return "", nil
	}}
	m := modelWithData(r)
	m.focus = panelWindows

	// r opens the prompt pre-filled with the current name.
	m, _ = update(m, key("r"))
	if m.mode != modePrompt {
		t.Fatalf("mode = %d, want modePrompt", m.mode)
	}
	if m.textInput.Value() != "code" {
		t.Errorf("prompt initial value = %q, want %q", m.textInput.Value(), "code")
	}

	// Type a new name and submit.
	m.textInput.SetValue("servers")
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeNormal {
		t.Error("enter should close the prompt")
	}
	run(cmd)
	found := false
	for _, c := range calls {
		if c == "rename-window -t @1 -- servers" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected rename-window -t @1 -- servers, got %v", calls)
	}
}

func TestEmptyPromptDoesNothing(t *testing.T) {
	r := funcRunner{fn: func(_ ...string) (string, error) { return "", nil }}
	m := modelWithData(r)
	m.focus = panelWindows
	m, _ = update(m, key("n")) // new window prompt
	m.textInput.SetValue("")
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeNormal {
		t.Error("enter should close the prompt")
	}
	if cmd != nil {
		t.Error("submitting an empty name should not run a command")
	}
}

func TestPromptEscCancels(t *testing.T) {
	r := funcRunner{fn: func(_ ...string) (string, error) { return "", nil }}
	m := modelWithData(r)
	m.focus = panelWindows
	m, _ = update(m, key("r"))
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != modeNormal {
		t.Error("esc should cancel the prompt")
	}
	if cmd != nil {
		t.Error("esc should not run a command")
	}
	if m.pending.op != opNone {
		t.Error("esc should clear the pending action")
	}
}

func TestAttachPreSelectsWindowAndPane(t *testing.T) {
	var calls []string
	r := funcRunner{fn: func(args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		return "", nil
	}}
	m := modelWithData(r)
	m.focus = panelPanes

	m, _ = update(m, key("enter"))
	if m.pendingAttach != "$1" {
		t.Fatalf("pendingAttach = %q, want $1", m.pendingAttach)
	}
	// Enter returns a tea.Sequence whose inner closures aren't run by invoking
	// the outer cmd, so exercise the pre-select command directly to assert its
	// tmux calls.
	run(selectTargetCmd(r, "@1", "%1"))
	var sawWin, sawPane bool
	for _, c := range calls {
		switch c {
		case "select-window -t @1":
			sawWin = true
		case "select-pane -t %1":
			sawPane = true
		}
	}
	if !sawWin || !sawPane {
		t.Errorf("pre-select did not target window+pane; calls=%v", calls)
	}
}

func TestCycleLayoutAdvances(t *testing.T) {
	var calls []string
	r := funcRunner{fn: func(args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		return "", nil
	}}
	m := modelWithData(r)
	m.focus = panelWindows
	// The cycle restarts per window, so the first L on a window always applies
	// cycleLayouts[0] — otherwise a shared index could re-apply the layout the
	// window already had and look like it did nothing.
	m, cmd := update(m, key("L"))
	if m.layoutIdx != 0 {
		t.Errorf("layoutIdx = %d, want 0 after the first L on this window", m.layoutIdx)
	}
	run(cmd)
	want := "select-layout -t @1 " + cycleLayouts[0]
	found := false
	for _, c := range calls {
		if c == want {
			found = true
		}
	}
	if !found {
		t.Errorf("expected %q, got %v", want, calls)
	}
}

func TestActionErrStored(t *testing.T) {
	m := New(nopRunner(), nil)
	m, _ = update(m, actionErrMsg{err: errTest})
	if !errors.Is(m.err, errTest) {
		t.Errorf("m.err = %v, want the action error", m.err)
	}
}

func TestSwapWindowReorder(t *testing.T) {
	var calls []string
	r := funcRunner{fn: func(args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		return "", nil
	}}
	m := modelWithData(r)
	m.windows = []tmux.WindowInfo{
		{Index: 0, ID: "@1", Name: "code"},
		{Index: 1, ID: "@2", Name: "server"},
	}
	m.focus = panelWindows
	m.cur[panelWindows] = 0

	// Swap down ('>')
	m, cmd := update(m, key(">"))
	if m.cur[panelWindows] != 1 {
		t.Errorf("cur[panelWindows] = %d, want 1 after swapping down", m.cur[panelWindows])
	}
	msg := run(cmd)
	if _, ok := msg.(windowsMsg); !ok {
		t.Fatalf("swap produced %T, want windowsMsg", msg)
	}
	want := "swap-window -s @1 -t @2"
	found := false
	for _, c := range calls {
		if c == want {
			found = true
		}
	}
	if !found {
		t.Errorf("expected %q, got %v", want, calls)
	}
}

func TestMoveWindowToSession(t *testing.T) {
	var calls []string
	r := funcRunner{fn: func(args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		return "", nil
	}}
	m := modelWithData(r)
	m.sessions = []sessions.Session{
		{ID: "$1", Name: "webapp"},
		{ID: "$2", Name: "infra"},
	}
	m.focus = panelWindows

	// 'W' opens session picker
	m, _ = update(m, key("W"))
	if m.mode != modeMoveWindow {
		t.Fatalf("mode = %d, want modeMoveWindow", m.mode)
	}
	if len(m.moveWindowSessions) != 1 || m.moveWindowSessions[0].ID != "$2" {
		t.Fatalf("moveWindowSessions = %+v, want only $2", m.moveWindowSessions)
	}

	// Press Enter to move
	m, cmd := update(m, key("enter"))
	if m.mode != modeNormal {
		t.Errorf("mode = %d, want modeNormal after enter", m.mode)
	}
	msg := run(cmd)
	if _, ok := msg.(windowsMsg); !ok {
		t.Fatalf("move produced %T, want windowsMsg", msg)
	}
	want := "move-window -s @1 -t $2:"
	found := false
	for _, c := range calls {
		if c == want {
			found = true
		}
	}
	if !found {
		t.Errorf("expected %q, got %v", want, calls)
	}
}

func TestSplitPaneVerticalFlow(t *testing.T) {
	var calls []string
	r := funcRunner{fn: func(args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "split-window" {
			return "%2\n", nil
		}
		return "", nil
	}}
	m := modelWithData(r)
	m.focus = panelPanes

	// 's' opens the command prompt
	m, _ = update(m, key("s"))
	if m.mode != modePrompt {
		t.Fatalf("mode = %d, want modePrompt", m.mode)
	}
	if !strings.Contains(m.promptTitle, "Split vertically") {
		t.Errorf("promptTitle = %q, want Split vertically", m.promptTitle)
	}

	// Submit text "htop"
	m.textInput.SetValue("htop")
	m, cmd := update(m, key("enter"))
	if m.mode != modeNormal {
		t.Errorf("mode = %d, want modeNormal after submitting", m.mode)
	}
	msg := run(cmd)
	if _, ok := msg.(panesMsg); !ok {
		t.Fatalf("split produced %T, want panesMsg", msg)
	}
	want := "split-window -P -F #{pane_id} -t %1 -v -- htop"
	found := false
	for _, c := range calls {
		if c == want {
			found = true
		}
	}
	if !found {
		t.Errorf("expected call %q, got %v", want, calls)
	}
}

func TestSplitPaneHorizontalFlow(t *testing.T) {
	var calls []string
	r := funcRunner{fn: func(args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "split-window" {
			return "%3\n", nil
		}
		return "", nil
	}}
	m := modelWithData(r)
	m.focus = panelWindows

	// 'S' opens horizontal split prompt
	m, _ = update(m, key("S"))
	if m.mode != modePrompt {
		t.Fatalf("mode = %d, want modePrompt", m.mode)
	}
	if !strings.Contains(m.promptTitle, "Split horizontally") {
		t.Errorf("promptTitle = %q, want Split horizontally", m.promptTitle)
	}

	// Submit empty text
	m, cmd := update(m, key("enter"))
	if m.mode != modeNormal {
		t.Errorf("mode = %d, want modeNormal after submitting", m.mode)
	}
	msg := run(cmd)
	if _, ok := msg.(panesMsg); !ok {
		t.Fatalf("split produced %T, want panesMsg", msg)
	}
	want := "split-window -P -F #{pane_id} -t %1 -h"
	found := false
	for _, c := range calls {
		if c == want {
			found = true
		}
	}
	if !found {
		t.Errorf("expected call %q, got %v", want, calls)
	}
}

var errTest = &stringErr{"kaboom"}

type stringErr struct{ s string }

func (e *stringErr) Error() string { return e.s }
