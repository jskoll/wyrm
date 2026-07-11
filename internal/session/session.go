// Package session builds and destroys tmux sessions from a config.
//
// Error policy: structural failures (creating the session or a window,
// killing a session) return errors; per-pane failures (splits, commands,
// hooks, layout) print a warning to stderr and continue, so one broken
// command doesn't abort the rest of the layout.
package session

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/jskoll/wyrm/internal/config"
	"github.com/jskoll/wyrm/internal/tmux"
)

// Create builds the session described by cfg and returns its name. If a
// session with that name is already running it is left untouched — running
// panes keep running — and created is false so the caller can attach to it.
func Create(r tmux.Runner, cfg *config.Config) (name string, created bool, err error) {
	name, root, err := cfg.Session.Resolve()
	if err != nil {
		return "", false, err
	}
	if len(cfg.Windows) == 0 {
		return "", false, fmt.Errorf("no windows defined in config")
	}

	// "=" forces an exact name match; bare -t would prefix-match.
	if _, err := r.Run("has-session", "-t", "="+name); err == nil {
		return name, false, nil
	}

	if err := runHook(cfg.Session.OnProjectStart, root); err != nil {
		warnf("on_project_start failed: %v", err)
	}

	for i, w := range cfg.Windows {
		var out string
		var err error
		if i == 0 {
			out, err = r.Run("new-session", "-d", "-P", "-F", "#{window_id}|#{pane_id}",
				"-s", name, "-n", w.Name, "-c", root)
			if err != nil {
				return "", false, fmt.Errorf("creating session: %v (%s)", err, out)
			}
		} else {
			out, err = r.Run("new-window", "-P", "-F", "#{window_id}|#{pane_id}",
				"-t", name, "-n", w.Name, "-c", root)
			if err != nil {
				return "", false, fmt.Errorf("creating window %q: %v (%s)", w.Name, err, out)
			}
		}
		windowID, paneID, ok := strings.Cut(out, "|")
		if !ok {
			return "", false, fmt.Errorf("unexpected tmux output %q", out)
		}
		fmt.Printf("window %s: %s\n", windowID, w.Name)
		buildWindow(r, windowID, paneID, w)
	}

	if cfg.Session.StartupWindow != "" {
		selectStartup(r, name, cfg.Session.StartupWindow, cfg.Session.StartupPane)
	}
	return name, true, nil
}

// Kill runs the on_project_exit hook and destroys the session. The hook is
// skipped when the session isn't running.
func Kill(r tmux.Runner, cfg *config.Config) (string, error) {
	name, root, err := cfg.Session.Resolve()
	if err != nil {
		return "", err
	}
	if _, err := r.Run("has-session", "-t", name); err != nil {
		return "", fmt.Errorf("session %q is not running", name)
	}
	if err := runHook(cfg.Session.OnProjectExit, root); err != nil {
		warnf("on_project_exit failed: %v", err)
	}
	if out, err := r.Run("kill-session", "-t", name); err != nil {
		return "", fmt.Errorf("killing session %q: %v (%s)", name, err, out)
	}
	return name, nil
}

func buildWindow(r tmux.Runner, windowID, initialPane string, w config.Window) {
	switch {
	case len(w.Splits) > 0:
		applySplits(r, initialPane, w.Splits, w.PreWindow)
	case len(w.Panes) > 0:
		applyPanes(r, windowID, initialPane, w)
	case w.PreWindow != "":
		sendKeys(r, initialPane, w.PreWindow)
	}
}

// applySplits walks a split tree. Each entry with a type splits the pane of
// the previous entry at the same level (the base pane for the first entry);
// entries without a type reuse that pane. Children operate within their
// parent's pane. Panes are addressed by tmux pane ID (%N), so the result is
// independent of the user's pane-base-index setting.
func applySplits(r tmux.Runner, basePane string, splits []config.Split, preWindow string) {
	current := basePane
	for _, s := range splits {
		pane := current
		if s.Type != "" {
			newPane, err := splitPane(r, current, s)
			if err != nil {
				warnf("failed to split pane: %v", err)
				continue
			}
			pane = newPane
		}
		sendKeys(r, pane, preWindow)
		sendKeys(r, pane, s.Command)
		applySplits(r, pane, s.Children, preWindow)
		current = pane
	}
}

func splitPane(r tmux.Runner, target string, s config.Split) (string, error) {
	dir := "-v"
	if t := strings.ToLower(s.Type); t == "h" || t == "horizontal" {
		dir = "-h"
	}
	args := []string{"split-window", "-t", target, dir, "-P", "-F", "#{pane_id}"}
	if s.Size > 0 {
		// -l N% rather than -p N: -p was deprecated in tmux 3.1 and removed
		// from newer builds; -l with a percentage works on 3.1+.
		args = append(args, "-l", fmt.Sprintf("%d%%", s.Size))
	}
	out, err := r.Run(args...)
	if err != nil {
		return "", fmt.Errorf("%v (%s)", err, out)
	}
	return out, nil
}

// applyPanes implements the legacy flat pane list: panes split alternately
// h/v off the previously created pane, then a layout evens them out.
func applyPanes(r tmux.Runner, windowID, initialPane string, w config.Window) {
	sendKeys(r, initialPane, w.PreWindow)
	sendKeys(r, initialPane, w.Panes[0].Command)

	current := initialPane
	for i, p := range w.Panes[1:] {
		dir := "-h"
		if i%2 == 1 {
			dir = "-v"
		}
		out, err := r.Run("split-window", "-t", current, dir, "-P", "-F", "#{pane_id}")
		if err != nil {
			warnf("failed to split pane: %v (%s)", err, out)
			continue
		}
		current = out
		sendKeys(r, current, w.PreWindow)
		sendKeys(r, current, p.Command)
	}

	layout := w.Layout
	if layout == "" && len(w.Panes) > 1 {
		layout = "tiled"
	}
	if layout != "" {
		if out, err := r.Run("select-layout", "-t", windowID, layout); err != nil {
			warnf("failed to apply layout %q: %v (%s)", layout, err, out)
		}
	}
}

// sendKeys types a command into the target pane. Commands starting with "#"
// are comments and are skipped.
func sendKeys(r tmux.Runner, target, command string) {
	if command == "" || strings.HasPrefix(command, "#") {
		return
	}
	if out, err := r.Run("send-keys", "-t", target, command, "Enter"); err != nil {
		warnf("failed to run %q in %s: %v (%s)", command, target, err, out)
	}
}

// startupWindowPattern rejects control characters and other input that can't
// be a window name or index.
var startupWindowPattern = regexp.MustCompile(`^[a-zA-Z0-9_:\-.\s()[\]{}]+$`)

func selectStartup(r tmux.Runner, session, window string, pane *int) {
	if !startupWindowPattern.MatchString(window) {
		warnf("invalid startup_window value: %q", window)
		return
	}
	if _, err := r.Run("select-window", "-t", session+":"+window); err != nil {
		warnf("failed to select window %q: %v", window, err)
		return
	}
	if pane != nil {
		target := fmt.Sprintf("%s:%s.%d", session, window, *pane)
		if _, err := r.Run("select-pane", "-t", target); err != nil {
			warnf("failed to select pane %d in window %q: %v", *pane, window, err)
		}
	}
}

// runHook executes a lifecycle hook via bash in the given directory.
func runHook(hook, dir string) error {
	if hook == "" {
		return nil
	}
	cmd := exec.Command("bash", "-c", hook)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func warnf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "wyrm: warning: "+format+"\n", args...)
}
