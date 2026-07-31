package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/jskoll/wyrm/internal/agent"
	"github.com/jskoll/wyrm/internal/config"
	"github.com/jskoll/wyrm/internal/sessions"
	"github.com/jskoll/wyrm/internal/tmux"
)

// agentRunner fakes a server with the given panes, returning canned capture
// output per pane ID.
func agentRunner(panes string, captures map[string]string) funcRunner {
	return funcRunner{fn: func(args ...string) (string, error) {
		switch {
		case args[0] == "list-panes" && len(args) > 1 && args[1] == "-a":
			return panes, nil
		case args[0] == "capture-pane":
			// capture-pane -p -t <id>
			id := args[len(args)-1]
			out, ok := captures[id]
			if !ok {
				return "", errors.New("no such pane")
			}
			return out, nil
		}
		return "", nil
	}}
}

const waitingPane = "│ Do you want to proceed?  │\n│ ❯ 1. Yes                 │\n│   2. No                  │"
const workingPane = "✻ Scurrying… (27s · ↓ 1.6k tokens)\n Esc to cancel · Tab to amend"
const donePane = "⏺ All set.\n✻ Worked for 8s\n❯\n  -- INSERT --"

func TestLoadAgentStatusClassifiesAndRollsUp(t *testing.T) {
	r := agentRunner(
		"$1|@1|%1|claude\n"+ // blocked
			"$1|@1|%2|zsh\n"+ // not an agent, never captured
			"$1|@2|%3|claude\n"+ // busy
			"$2|@3|%4|claude\n", // idle
		map[string]string{"%1": waitingPane, "%3": workingPane, "%4": donePane},
	)

	msg, ok := run(loadAgentStatus(r, nil, "")).(agentStatusMsg)
	if !ok {
		t.Fatal("expected an agentStatusMsg")
	}
	if msg.err != nil {
		t.Fatalf("unexpected error: %v", msg.err)
	}

	if got := msg.status.pane("%1"); got != agent.StateBlocked {
		t.Errorf("%%1 = %v, want blocked", got)
	}
	if got := msg.status.pane("%3"); got != agent.StateBusy {
		t.Errorf("%%3 = %v, want busy", got)
	}
	if got := msg.status.pane("%4"); got != agent.StateIdle {
		t.Errorf("%%4 = %v, want idle", got)
	}
	if got := msg.status.pane("%2"); got != agent.StateNone {
		t.Errorf("a shell pane = %v, want none", got)
	}

	// Windows and sessions take the state of their most urgent pane.
	if got := msg.status.window("@1"); got != agent.StateBlocked {
		t.Errorf("@1 = %v, want blocked", got)
	}
	if got := msg.status.session("$1"); got != agent.StateBlocked {
		t.Errorf("$1 = %v, want blocked (it holds a blocked pane)", got)
	}
	if got := msg.status.session("$2"); got != agent.StateIdle {
		t.Errorf("$2 = %v, want idle", got)
	}
}

// Only agent panes are captured — the expensive call must not be made for every
// shell on the server.
func TestLoadAgentStatusCapturesOnlyAgentPanes(t *testing.T) {
	captured := []string{}
	r := funcRunner{fn: func(args ...string) (string, error) {
		switch args[0] {
		case "list-panes":
			return "$1|@1|%1|claude\n$1|@1|%2|zsh\n$1|@1|%3|vim\n", nil
		case "capture-pane":
			captured = append(captured, args[len(args)-1])
		}
		return "", nil
	}}

	run(loadAgentStatus(r, nil, ""))

	if len(captured) != 1 || captured[0] != "%1" {
		t.Errorf("captured %v, want only [%%1]", captured)
	}
}

// The TUI's own pane is never captured, mirroring the preview's self-check.
func TestLoadAgentStatusSkipsSelfPane(t *testing.T) {
	captured := []string{}
	r := funcRunner{fn: func(args ...string) (string, error) {
		switch args[0] {
		case "list-panes":
			return "$1|@1|%1|claude\n$1|@1|%9|claude\n", nil
		case "capture-pane":
			captured = append(captured, args[len(args)-1])
		}
		return "", nil
	}}

	run(loadAgentStatus(r, nil, "%9"))

	for _, id := range captured {
		if id == "%9" {
			t.Error("the TUI's own pane must not be captured")
		}
	}
}

// A pane that dies between listing and capturing is ordinary; the rest of the
// scan must still complete.
func TestLoadAgentStatusToleratesAVanishedPane(t *testing.T) {
	r := agentRunner(
		"$1|@1|%1|claude\n$1|@1|%2|claude\n",
		map[string]string{"%2": waitingPane}, // %1 errors
	)

	msg := run(loadAgentStatus(r, nil, "")).(agentStatusMsg)
	if msg.err != nil {
		t.Fatalf("one dead pane should not fail the scan: %v", msg.err)
	}
	if got := msg.status.pane("%2"); got != agent.StateBlocked {
		t.Errorf("%%2 = %v, want blocked", got)
	}
}

func TestAgentCommandsAreConfigurable(t *testing.T) {
	r := agentRunner("$1|@1|%1|aider\n", map[string]string{"%1": waitingPane})

	if got := run(loadAgentStatus(r, nil, "")).(agentStatusMsg).status.pane("%1"); got != agent.StateNone {
		t.Errorf("aider with default commands = %v, want none", got)
	}
	if got := run(loadAgentStatus(r, []string{"aider"}, "")).(agentStatusMsg).status.pane("%1"); got != agent.StateBlocked {
		t.Errorf("aider with commands=[aider] = %v, want blocked", got)
	}
}

// A failed scan leaves the previous markers alone rather than clearing them.
func TestFailedScanKeepsPreviousMarkers(t *testing.T) {
	m := New(nopRunner(), nil)
	m.agents = agentStatus{sessions: map[string]agent.State{"$1": agent.StateBlocked}}

	m, _ = update(m, agentStatusMsg{err: errors.New("tmux went away")})

	if got := m.agents.session("$1"); got != agent.StateBlocked {
		t.Errorf("session state = %v, want the previous blocked to survive", got)
	}
}

// Detection can be switched off entirely, and then costs nothing.
func TestAgentScanDisabledBySettings(t *testing.T) {
	off := false
	s := &config.Settings{TUI: config.TUI{Agent: config.Agent{Enabled: &off}}}
	m := New(nopRunner(), s)

	if m.agentCmd() != nil {
		t.Error("agent scanning should produce no command when disabled")
	}
}

// The markers have to actually reach the screen, on every panel that can carry
// one.
func TestMarkersRenderInEveryPanel(t *testing.T) {
	m := mouseModel(t)
	m.agents = agentStatus{
		panes:    map[string]agent.State{"%1": agent.StateBlocked},
		windows:  map[string]agent.State{"@1": agent.StateBlocked},
		sessions: map[string]agent.State{"$2": agent.StateIdle},
	}
	out := m.View()

	if n := strings.Count(out, blockedGlyph); n < 2 {
		t.Errorf("blocked glyph appears %d times, want it on the window and pane rows\n%s", n, out)
	}
	// $2 is both a session ("two") and the running project "beta".
	if n := strings.Count(out, idleGlyph); n < 2 {
		t.Errorf("idle glyph appears %d times, want it on the session and project rows\n%s", n, out)
	}
}

// A busy agent gets no marker: an indicator lit on every agent pane all the
// time is one nobody reads.
func TestBusyAgentIsNotMarked(t *testing.T) {
	m := mouseModel(t)
	m.agents = agentStatus{
		sessions: map[string]agent.State{"$1": agent.StateBusy},
	}
	out := m.View()

	if strings.Contains(out, blockedGlyph) || strings.Contains(out, idleGlyph) {
		t.Errorf("a busy agent should carry no marker\n%s", out)
	}
}

// Projects show their session's state, so an agent waiting in a session that
// isn't selected is still visible.
func TestProjectRowShowsItsSessionState(t *testing.T) {
	m := mouseModel(t)
	m.sessions = []sessions.Session{{ID: "$2", Name: "two"}}
	m.windows, m.panes = nil, nil
	m.agents = agentStatus{sessions: map[string]agent.State{"$2": agent.StateBlocked}}

	out := m.View()
	if !strings.Contains(out, blockedGlyph) {
		t.Errorf("the running project's row should carry its session's marker\n%s", out)
	}
}

// ListAllPanes is the one call the whole scan is built on; a malformed line
// must be reported rather than silently producing a half-filled PaneRef.
func TestListAllPanesRejectsMalformedOutput(t *testing.T) {
	r := funcRunner{fn: func(_ ...string) (string, error) { return "$1|@1|%1\n", nil }}
	if _, err := tmux.ListAllPanes(r); err == nil {
		t.Error("expected an error for a short list-panes line")
	}
}

func TestListAllPanesNoServer(t *testing.T) {
	r := funcRunner{fn: func(_ ...string) (string, error) {
		return "no server running on /tmp/tmux-1000/default", errors.New("exit status 1")
	}}
	refs, err := tmux.ListAllPanes(r)
	if err != nil || refs != nil {
		t.Errorf("no server should be an empty result, got %v / %v", refs, err)
	}
}
