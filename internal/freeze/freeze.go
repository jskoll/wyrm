// Package freeze snapshots a running tmux session's windows, split layout,
// and foreground pane commands into a wyrm config.Config — the reverse of
// internal/session's Create. tmux keeps no record of what was originally
// typed into a pane, so a pane's command is captured as whatever program is
// currently running in its foreground (#{pane_current_command}), the same
// approach tmuxp's "freeze" uses.
package freeze

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jskoll/wyrm/internal/config"
	"github.com/jskoll/wyrm/internal/tmux"
)

// shells are the pane commands treated as "no command": a pane sitting at an
// idle prompt. Writing the shell's own name as the pane's command would type
// "zsh"↵ into a shell on every rebuild, nesting a second one in every idle
// pane — so leaving the session then took two exits per pane.
var shells = map[string]bool{
	"bash": true, "zsh": true, "sh": true, "fish": true,
	"dash": true, "ksh": true, "tcsh": true, "csh": true, "nu": true,
	"elvish": true, "xonsh": true, "ash": true,
}

// isShell reports whether a pane's foreground command is just a shell prompt.
// $SHELL is honored too, for a shell not in the list above.
func isShell(command string) bool {
	name := strings.TrimPrefix(strings.TrimSpace(command), "-") // login shells appear as "-zsh"
	if name == "" {
		return true
	}
	if shells[name] {
		return true
	}
	return filepath.Base(os.Getenv("SHELL")) == name
}

// paneCommand is the command to record for a pane, blank for an idle shell.
func paneCommand(commands map[string]string, paneID string) string {
	cmd := commands[paneID]
	if isShell(cmd) {
		return ""
	}
	return cmd
}

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
