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

// pl builds one list-panes -a fixture line in tmux.ListAllPanes' format
// (session_id, session_name, window_id, window_index, window_name, pane_id,
// pane_index, command, \x01-separated). Session/window names and indices
// are irrelevant to agent detection, so this fills in placeholders and
// varies only the IDs and command every caller here actually cares about.
func pl(sessionID, windowID, paneID, command string) string {
	return sessionID + "\x01s\x01" + windowID + "\x010\x01w\x01" + paneID + "\x010\x01" + command + "\n"
}

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
		pl("$1", "@1", "%1", "claude")+ // blocked
			pl("$1", "@1", "%2", "zsh")+ // not an agent, never captured
			pl("$1", "@2", "%3", "claude")+ // busy
			pl("$2", "@3", "%4", "claude"), // idle
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
			return pl("$1", "@1", "%1", "claude") + pl("$1", "@1", "%2", "zsh") + pl("$1", "@1", "%3", "vim"), nil
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
			return pl("$1", "@1", "%1", "claude") + pl("$1", "@1", "%9", "claude"), nil
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
		pl("$1", "@1", "%1", "claude")+pl("$1", "@1", "%2", "claude"),
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

// tui.agent.commands widens which panes the *built-in* detector inspects, for
// someone running Claude Code under a wrapper name. The patterns stay the
// shipped ones, which is the whole point of the distinction from profiles.
func TestAgentCommandsWidenTheBuiltInProfile(t *testing.T) {
	r := agentRunner(pl("$1", "@1", "%1", "myclaude"), map[string]string{"%1": waitingPane})

	def, err := agentProfiles(nil)
	if err != nil {
		t.Fatalf("agentProfiles(nil): %v", err)
	}
	if got := run(loadAgentStatus(r, def, "")).(agentStatusMsg).status.pane("%1"); got != agent.StateNone {
		t.Errorf("an unknown command = %v, want none", got)
	}

	widened, err := agentProfiles(&config.Settings{
		TUI: config.TUI{Agent: config.Agent{Commands: []string{"myclaude"}}},
	})
	if err != nil {
		t.Fatalf("agentProfiles: %v", err)
	}
	if got := run(loadAgentStatus(r, widened, "")).(agentStatusMsg).status.pane("%1"); got != agent.StateBlocked {
		t.Errorf("widened commands = %v, want blocked via the built-in patterns", got)
	}
}

// A profile describes a different agent's chrome. It replaces the built-in one
// rather than adding to it, so Claude Code's markers can't decide another
// agent's state.
func TestAgentProfilesClassifyByTheirOwnPatterns(t *testing.T) {
	const aiderBusy = "> refactor the parser\napplying edits… 12 files\n[working]"
	const aiderIdle = "> refactor the parser\napplied 3 edits\naider> "
	r := agentRunner(
		pl("$1", "@1", "%1", "aider")+pl("$1", "@1", "%2", "claude"),
		map[string]string{"%1": aiderBusy, "%2": waitingPane},
	)

	profiles, err := agentProfiles(&config.Settings{TUI: config.TUI{Agent: config.Agent{
		Profiles: []config.AgentProfile{{
			Commands: []string{"aider"},
			Busy:     []string{"[working]"},
			Idle:     []string{"aider> "},
		}},
	}}})
	if err != nil {
		t.Fatalf("agentProfiles: %v", err)
	}

	status := run(loadAgentStatus(r, profiles, "")).(agentStatusMsg).status
	if got := status.pane("%1"); got != agent.StateBusy {
		t.Errorf("aider pane = %v, want busy from its own patterns", got)
	}
	// claude isn't in any configured profile, so it isn't an agent pane at all.
	if got := status.pane("%2"); got != agent.StateNone {
		t.Errorf("claude pane = %v, want none — profiles replace the built-in one", got)
	}

	// And the idle side, on evidence rather than by elimination.
	r2 := agentRunner(pl("$1", "@1", "%1", "aider"), map[string]string{"%1": aiderIdle})
	if got := run(loadAgentStatus(r2, profiles, "")).(agentStatusMsg).status.pane("%1"); got != agent.StateIdle {
		t.Errorf("aider at its prompt = %v, want idle", got)
	}
}

// A profile wyrm can't use is an error, not a silent fallback: markers that
// quietly stopped appearing look exactly like an agent that never waits for you.
func TestAgentProfilesRejectBadConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		profile config.AgentProfile
	}{
		{"no commands", config.AgentProfile{Busy: []string{"x"}}},
		{"uncompilable pattern", config.AgentProfile{Commands: []string{"a"}, BusyPattern: "("}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := agentProfiles(&config.Settings{
				TUI: config.TUI{Agent: config.Agent{Profiles: []config.AgentProfile{tt.profile}}},
			})
			if err == nil {
				t.Fatal("want an error naming the bad profile")
			}
			if !strings.Contains(err.Error(), "profiles[0]") {
				t.Errorf("err = %q, want it to say which profile", err)
			}
		})
	}
}

// A custom busy_pattern compiles and matches.
func TestAgentProfileBusyPattern(t *testing.T) {
	profiles, err := agentProfiles(&config.Settings{TUI: config.TUI{Agent: config.Agent{
		Profiles: []config.AgentProfile{{Commands: []string{"bot"}, BusyPattern: `thinking \d+s`}},
	}}})
	if err != nil {
		t.Fatalf("agentProfiles: %v", err)
	}
	r := agentRunner(pl("$1", "@1", "%1", "bot"), map[string]string{"%1": "output\nthinking 42s"})
	if got := run(loadAgentStatus(r, profiles, "")).(agentStatusMsg).status.pane("%1"); got != agent.StateBusy {
		t.Errorf("pane = %v, want busy from the custom pattern", got)
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

// batchingAgentRunner answers capture-pane in batches, so a test can see that
// the scan reads every pane in one process rather than one process per pane.
type batchingAgentRunner struct {
	funcRunner
	batches  int
	direct   int
	failPane string
}

func (b *batchingAgentRunner) Run(args ...string) (string, error) {
	if args[0] == "capture-pane" {
		b.direct++
	}
	return b.funcRunner.Run(args...)
}

func (b *batchingAgentRunner) RunBatch(cmds [][]string) ([]string, error) {
	b.batches++
	outs := make([]string, 0, len(cmds))
	for _, c := range cmds {
		if b.failPane != "" && c[len(c)-1] == b.failPane {
			return outs, errors.New("can't find pane")
		}
		out, err := b.funcRunner.Run(c...)
		if err != nil {
			return outs, err
		}
		outs = append(outs, out)
	}
	return outs, nil
}

// The scan runs every listRefreshInterval for as long as the TUI is open, so
// reading N panes used to cost N tmux processes on a timer. Now it costs one.
func TestAgentScanBatchesCaptures(t *testing.T) {
	r := &batchingAgentRunner{funcRunner: agentRunner(
		pl("$1", "@1", "%1", "claude")+pl("$1", "@1", "%2", "claude")+pl("$1", "@2", "%3", "claude")+pl("$1", "@2", "%4", "zsh"),
		map[string]string{"%1": waitingPane, "%2": workingPane, "%3": donePane},
	)}

	status := run(loadAgentStatus(r, nil, "")).(agentStatusMsg).status
	if r.batches != 1 {
		t.Errorf("issued %d batches, want 1", r.batches)
	}
	if r.direct != 0 {
		t.Errorf("issued %d capture-pane calls outside the batch, want 0", r.direct)
	}
	// The shell pane is never captured, so the batch holds only the agents.
	if got := status.pane("%1"); got != agent.StateBlocked {
		t.Errorf("%%1 = %v, want blocked", got)
	}
	if got := status.pane("%3"); got != agent.StateIdle {
		t.Errorf("%%3 = %v, want idle", got)
	}
}

// A pane that closed between the listing and the capture stops the batch there.
// The panes after it must still be read, or one dead pane would blank every
// marker behind it.
func TestAgentScanSurvivesAPaneDyingMidBatch(t *testing.T) {
	r := &batchingAgentRunner{
		funcRunner: agentRunner(
			pl("$1", "@1", "%1", "claude")+pl("$1", "@1", "%2", "claude")+pl("$1", "@2", "%3", "claude"),
			map[string]string{"%1": waitingPane, "%3": donePane},
		),
		failPane: "%2",
	}

	status := run(loadAgentStatus(r, nil, "")).(agentStatusMsg).status
	if got := status.pane("%1"); got != agent.StateBlocked {
		t.Errorf("%%1 = %v, want blocked (it was read before the failure)", got)
	}
	if got := status.pane("%3"); got != agent.StateIdle {
		t.Errorf("%%3 = %v, want idle (it must be replayed after the failure)", got)
	}
	if got := status.pane("%2"); got != agent.StateNone {
		t.Errorf("%%2 = %v, want none — the pane is gone", got)
	}
}
