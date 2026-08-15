package tui

import (
	"testing"

	"github.com/jskoll/wyrm/internal/agent"
	"github.com/jskoll/wyrm/internal/config"
)

func TestAgentTransitionNotification(t *testing.T) {
	tTrue := true
	settings := &config.Settings{
		TUI: config.TUI{
			Agent: config.Agent{
				Notify: config.AgentNotify{
					Enabled:   &tTrue,
					OnBlocked: &tTrue,
					OnIdle:    &tTrue,
				},
			},
		},
	}

	m := New(nopRunner(), settings)
	m.prevPaneStates = map[string]agent.State{
		"%1": agent.StateNone,
	}

	// Transition to Blocked
	newStatus := agentStatus{
		panes: map[string]agent.State{
			"%1": agent.StateBlocked,
		},
	}

	m, cmd := update(m, agentStatusMsg{status: newStatus})
	if cmd == nil {
		t.Fatalf("expected non-nil notification command on transition to Blocked")
	}
	if m.prevPaneStates["%1"] != agent.StateBlocked {
		t.Errorf("expected prevPaneStates to be updated to Blocked")
	}
}
