package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jskoll/wyrm/internal/agent"
	"github.com/jskoll/wyrm/internal/tmux"
)

// maxAgentCaptures bounds how many panes one refresh will capture. Finding the
// candidates costs a single list-panes call, but reading each one costs a
// capture-pane, and this runs on a timer: someone with a wall of agent panes
// should get a slightly incomplete picture rather than a tmux call storm every
// few seconds.
const maxAgentCaptures = 16

// agentStatus holds the detected state of every agent pane, rolled up to the
// windows and sessions that contain them so all four panels can be marked from
// one pass. The maps are keyed by tmux ID and are nil until the first scan
// lands — a nil map reads as StateNone, which is the right answer before
// anything is known.
type agentStatus struct {
	panes    map[string]agent.State
	windows  map[string]agent.State
	sessions map[string]agent.State
}

func (a agentStatus) pane(id string) agent.State    { return a.panes[id] }
func (a agentStatus) window(id string) agent.State  { return a.windows[id] }
func (a agentStatus) session(id string) agent.State { return a.sessions[id] }

// agentStatusMsg carries a completed scan. An error is deliberately not
// reported to the footer: this is a background poll of an optional decoration,
// and a transient tmux hiccup shouldn't stamp on an error the user was reading.
// A failed scan just leaves the previous markers in place.
type agentStatusMsg struct {
	status agentStatus
	err    error
}

// loadAgentStatus scans the whole server for agent panes and classifies each.
//
// It takes one list-panes call plus one capture-pane per agent pane — which is
// why the candidate filter happens before any capture, and why a pane running
// an ordinary shell is never captured at all.
func loadAgentStatus(r tmux.Runner, commands []string, skipPane string) tea.Cmd {
	return func() tea.Msg {
		refs, err := tmux.ListAllPanes(r)
		if err != nil {
			return agentStatusMsg{err: err}
		}
		status := agentStatus{
			panes:    map[string]agent.State{},
			windows:  map[string]agent.State{},
			sessions: map[string]agent.State{},
		}
		captures := 0
		for _, ref := range refs {
			if !agent.IsAgent(ref.Command, commands) {
				continue
			}
			// The pane wyrm tui is running in is never an agent pane, but skip
			// it explicitly anyway: capturing it is the same mirror-of-a-mirror
			// the preview avoids.
			if skipPane != "" && ref.PaneID == skipPane {
				continue
			}
			if captures >= maxAgentCaptures {
				break
			}
			captures++
			content, err := tmux.CapturePanePlain(r, ref.PaneID)
			if err != nil {
				// A pane that died between listing and capturing is ordinary;
				// leave it unmarked and carry on with the rest.
				continue
			}
			state := agent.Detect(ref.Command, content, commands)
			if state == agent.StateNone {
				continue
			}
			status.panes[ref.PaneID] = state
			status.windows[ref.WindowID] = agent.Merge(status.windows[ref.WindowID], state)
			status.sessions[ref.SessionID] = agent.Merge(status.sessions[ref.SessionID], state)
		}
		return agentStatusMsg{status: status}
	}
}

// agentCmd returns the scan command, or nil when detection is switched off.
func (m Model) agentCmd() tea.Cmd {
	if !m.settings.AgentEnabled() {
		return nil
	}
	return loadAgentStatus(m.runner, m.settings.AgentCommands(), m.selfPane)
}

// agentMark returns the trailing status span for a row, and whether there is
// one. Busy panes get nothing: see agent.State.NeedsUser.
func agentMark(state agent.State) (span, bool) {
	switch state {
	case agent.StateBlocked:
		return span{blockedMark, " " + blockedGlyph}, true
	case agent.StateIdle:
		return span{idleMark, " " + idleGlyph}, true
	}
	return span{}, false
}

// The markers. Two glyphs rather than one because the two states want different
// things from the user: "⏸" is an agent stopped on a question it can't answer
// itself, "✓" one that finished and is waiting for the next instruction.
const (
	blockedGlyph = "⏸"
	idleGlyph    = "✓"
)
