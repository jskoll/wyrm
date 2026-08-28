package agent

import "github.com/jskoll/wyrm/internal/tmux"

// MaxCaptures bounds how many panes one scan will capture. Finding the
// candidates costs a single list-panes call, but reading each one costs a
// capture-pane, and both callers run on a timer: someone with a wall of agent
// panes should get a slightly incomplete picture rather than a tmux call storm
// every few seconds.
//
// The TUI has had this bound since it grew agent markers. `wyrm status` did
// not, so `wyrm status --watch` on a machine with 40 agent panes issued 40
// capture-pane calls every 2 seconds — the exact storm this constant exists to
// prevent, against a server the user is working in. Both callers now select
// candidates through Candidates so the bound cannot drift again.
const MaxCaptures = 16

// Candidates picks the agent panes worth capturing from refs, in list order,
// stopping at max. skipPane, when non-empty, is excluded: the pane the caller
// is itself rendering in is never worth capturing, and reading it is the
// mirror-of-a-mirror the TUI's preview avoids.
//
// skipped reports how many further agent panes were left unscanned, so a
// caller can say the picture is partial instead of quietly under-reporting.
// A max of zero or less means no bound.
func Candidates(refs []tmux.PaneRef, profiles []Profile, skipPane string, max int) (selected []tmux.PaneRef, skipped int) {
	for _, ref := range refs {
		if !IsAgentPane(ref.Command, profiles) {
			continue
		}
		if skipPane != "" && ref.PaneID == skipPane {
			continue
		}
		if max > 0 && len(selected) >= max {
			skipped++
			continue
		}
		selected = append(selected, ref)
	}
	return selected, skipped
}
