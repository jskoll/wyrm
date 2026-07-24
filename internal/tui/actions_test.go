package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jskoll/wyrm/internal/picker"
	"github.com/jskoll/wyrm/internal/tmux"
)

// modelWithData returns a model wired to a recording runner with one session,
// one window, and one pane selected.
func modelWithData(r tmux.Runner) Model {
	m := New(r, nil)
	m.sessions = []picker.Session{{ID: "$1", Name: "webapp"}}
	m.windows = []tmux.WindowInfo{{Index: 0, ID: "@1", Name: "code"}}
	m.panes = []tmux.PaneInfo{{ID: "%1", Index: 0, Command: "nvim"}}
	m.sessionCur, m.windowCur, m.paneCur = 0, 0, 0
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
		if c == "rename-window -t @1 servers" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected rename-window -t @1 servers, got %v", calls)
	}
}

func TestEmptyPromptDoesNothing(t *testing.T) {
	r := funcRunner{fn: func(args ...string) (string, error) { return "", nil }}
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
	r := funcRunner{fn: func(args ...string) (string, error) { return "", nil }}
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
	m, cmd := update(m, key("L"))
	if m.layoutIdx != 1 {
		t.Errorf("layoutIdx = %d, want 1 after one L", m.layoutIdx)
	}
	run(cmd)
	want := "select-layout -t @1 " + cycleLayouts[1]
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
	if m.err != errTest {
		t.Errorf("m.err = %v, want the action error", m.err)
	}
}

var errTest = &stringErr{"kaboom"}

type stringErr struct{ s string }

func (e *stringErr) Error() string { return e.s }
