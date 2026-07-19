// Package freeze snapshots a running tmux session's windows, split layout,
// and foreground pane commands into a wyrm config.Config — the reverse of
// internal/session's Create. tmux keeps no record of what was originally
// typed into a pane, so a pane's command is captured as whatever program is
// currently running in its foreground (#{pane_current_command}), the same
// approach tmuxp's "freeze" uses.
package freeze

import (
	"fmt"

	"github.com/jskoll/wyrm/internal/config"
	"github.com/jskoll/wyrm/internal/tmux"
)

// Config builds a wyrm config.Config snapshotting the live layout of the
// tmux session identified by sessionID (a tmux session ID, e.g. "$3").
// name and root are written into the resulting [session] block as-is.
func Config(r tmux.Runner, sessionID, name, root string) (*config.Config, error) {
	windows, err := tmux.ListWindows(r, sessionID)
	if err != nil {
		return nil, err
	}
	if len(windows) == 0 {
		return nil, fmt.Errorf("session %q has no windows", name)
	}

	cfg := &config.Config{
		Session: config.Session{Name: name, Root: root},
	}

	for _, w := range windows {
		panes, err := tmux.ListPanes(r, w.ID)
		if err != nil {
			return nil, fmt.Errorf("window %q: %w", w.Name, err)
		}
		commands := make(map[string]string, len(panes))
		var activePane int
		haveActivePane := false
		for _, p := range panes {
			commands[p.ID] = p.Command
			if p.Active {
				activePane, haveActivePane = p.Index, true
			}
		}

		layoutRoot, err := parseWindowLayout(w.Layout)
		if err != nil {
			return nil, fmt.Errorf("window %q: %w", w.Name, err)
		}

		cfg.Windows = append(cfg.Windows, config.Window{
			Name:   w.Name,
			Splits: splitsFromNode(layoutRoot, commands),
		})

		if w.Active {
			cfg.Session.StartupWindow = w.Name
			if haveActivePane {
				pane := activePane
				cfg.Session.StartupPane = &pane
			}
		}
	}

	return cfg, nil
}
