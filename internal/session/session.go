// Package session builds and destroys tmux sessions from a config.
//
// Error policy: structural failures (creating the session or a window,
// killing a session) return errors; per-pane failures (splits, commands,
// hooks, layout) print a warning to stderr and continue, so one broken
// command doesn't abort the rest of the layout.
package session

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/jskoll/wyrm/internal/config"
	"github.com/jskoll/wyrm/internal/tmux"
)

// Create builds the session described by cfg and returns its name and tmux
// session ID (e.g. "$3"). If a session with that name is already running it
// is left untouched — running panes keep running — and created is false so
// the caller can attach to it.
//
// Every tmux command below targets the session by ID once one is known
// (from FindSessionID, or captured off the initial new-session call), never
// by the raw config-derived name: tmux's -t target syntax treats "." as the
// window.pane separator, so a name containing "." (e.g. "wyrm.vim") would be
// misparsed by has-session, new-window, and friends. See tmux.FindSessionID.
//
// Per-window creation progress goes to stdout and per-pane warnings (see the
// package doc's error policy) go to stderr — passed in rather than hardcoded
// so callers can capture or redirect them, the same way main.run threads
// stdout/stderr throughout the CLI.
func Create(r tmux.Runner, cfg *config.Config, stdout, stderr io.Writer) (name, sessionID string, created bool, err error) {
	name, root, err := cfg.Session.Resolve(cfg.Dir())
	if err != nil {
		return "", "", false, err
	}
	if len(cfg.Windows) == 0 {
		return "", "", false, fmt.Errorf("no windows defined in config")
	}

	if id, ok, ferr := tmux.FindSessionID(r, name); ferr != nil {
		return "", "", false, ferr
	} else if ok {
		return name, id, false, nil
	}

	if err := runHook(cfg.Session.OnProjectStart, root, "on_project_start", stderr); err != nil {
		warnf(stderr, "on_project_start failed: %v", err)
	}

	var id, firstWindowID string
	for i, w := range cfg.Windows {
		var out string
		var err error
		var windowID, paneID string
		if i == 0 {
			// #{session_name} is captured alongside the IDs because tmux does
			// not always name the session what we asked for: some builds
			// replace "." and ":" with "_", and a config in a directory called
			// "example.com" hits that. Reporting the config's name instead of
			// the real one makes the *next* run fail to find the session and
			// try to create a duplicate.
			out, err = r.Run("new-session", "-d", "-P", "-F", "#{session_id}|#{session_name}|#{window_id}|#{pane_id}",
				"-s", name, "-n", w.Name, "-c", root)
			if err != nil {
				return "", "", false, fmt.Errorf("creating session: %v (%s)", err, out)
			}
			parts := strings.SplitN(out, "|", 4)
			if len(parts) != 4 {
				return "", "", false, fmt.Errorf("unexpected tmux output %q", out)
			}
			id, windowID, paneID = parts[0], parts[2], parts[3]
			if err := checkIDs(id, windowID, paneID); err != nil {
				return "", "", false, fmt.Errorf("creating session: %w", err)
			}
			if parts[1] != name {
				warnf(stderr, "tmux named the session %q, not %q", parts[1], name)
				name = parts[1]
			}
			firstWindowID = windowID
		} else {
			// -d keeps the session's active window where it started. Without
			// it tmux makes each new window current, so a freshly built
			// session opens on the *last* window in the config rather than the
			// first (which is what startup_window's default documents).
			out, err = r.Run("new-window", "-d", "-P", "-F", "#{window_id}|#{pane_id}",
				"-t", id, "-n", w.Name, "-c", root)
			if err != nil {
				return "", "", false, rollback(r, id, stderr,
					fmt.Errorf("creating window %q: %v (%s)", w.Name, err, out))
			}
			var ok bool
			windowID, paneID, ok = strings.Cut(out, "|")
			if !ok {
				return "", "", false, rollback(r, id, stderr, fmt.Errorf("unexpected tmux output %q", out))
			}
			if err := checkIDs("", windowID, paneID); err != nil {
				return "", "", false, rollback(r, id, stderr, fmt.Errorf("creating window %q: %w", w.Name, err))
			}
		}
		_, _ = fmt.Fprintf(stdout, "window %s: %s\n", windowID, w.Name)
		buildWindow(r, windowID, paneID, w, stderr)
	}

	if cfg.Session.StartupWindow != "" {
		selectStartup(r, id, cfg.Session.StartupWindow, cfg.Session.StartupPane, stderr)
	} else if firstWindowID != "" {
		// Every window was created with -d, so window 0 is still current — but
		// say so explicitly rather than relying on that, and land on its first
		// pane too (splits are also created with -d).
		if _, err := r.Run("select-window", "-t", firstWindowID); err != nil {
			warnf(stderr, "failed to select the first window: %v", err)
		}
	}
	return name, id, true, nil
}

// checkIDs validates the object IDs parsed out of a tmux "-F" response. An
// empty argument is skipped, so callers can check only the ones they parsed.
// See tmux.CheckID for why a malformed ID has to be caught here rather than
// left to fail later.
func checkIDs(sessionID, windowID, paneID string) error {
	if sessionID != "" {
		if err := tmux.CheckID(tmux.SessionSigil, "session", sessionID); err != nil {
			return err
		}
	}
	if windowID != "" {
		if err := tmux.CheckID(tmux.WindowSigil, "window", windowID); err != nil {
			return err
		}
	}
	if paneID != "" {
		if err := tmux.CheckID(tmux.PaneSigil, "pane", paneID); err != nil {
			return err
		}
	}
	return nil
}

// rollback destroys a session whose build failed partway through and returns
// cause unchanged. Without it the half-built session stays running, and the
// next `wyrm` finds it, reports "already running, attaching", and hands over a
// session missing most of its windows with no sign anything went wrong.
func rollback(r tmux.Runner, sessionID string, stderr io.Writer, cause error) error {
	if out, err := r.Run("kill-session", "-t", sessionID); err != nil {
		warnf(stderr, "could not clean up the partially built session: %v (%s)", err, out)
	}
	return cause
}

// Kill runs the on_project_exit hook and destroys the session. The hook is
// skipped when the session isn't running. Hook-failure warnings go to
// stderr, passed in rather than hardcoded — see Create.
func Kill(r tmux.Runner, cfg *config.Config, stderr io.Writer) (string, error) {
	name, root, err := cfg.Session.Resolve(cfg.Dir())
	if err != nil {
		return "", err
	}
	id, ok, err := tmux.FindSessionID(r, name)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("session %q is not running", name)
	}
	if err := runHook(cfg.Session.OnProjectExit, root, "on_project_exit", stderr); err != nil {
		warnf(stderr, "on_project_exit failed: %v", err)
	}
	if out, err := r.Run("kill-session", "-t", id); err != nil {
		return "", fmt.Errorf("killing session %q: %v (%s)", name, err, out)
	}
	return name, nil
}

func buildWindow(r tmux.Runner, windowID, initialPane string, w config.Window, stderr io.Writer) {
	// done tracks the panes pre_window has already been typed into, so it runs
	// exactly once per pane across the whole window — see sendPreWindow.
	done := map[string]bool{}
	switch {
	case len(w.Splits) > 0:
		applySplits(r, initialPane, w.Splits, w.PreWindow, done, stderr)
	case len(w.Panes) > 0:
		applyPanes(r, windowID, initialPane, w, done, stderr)
	case w.PreWindow != "":
		sendPreWindow(r, initialPane, w.PreWindow, done, stderr)
	}
}

// applySplits walks a split tree. Each entry with a type splits the pane of
// the previous entry at the same level (the base pane for the first entry);
// entries without a type reuse that pane. Children operate within their
// parent's pane. Panes are addressed by tmux pane ID (%N), so the result is
// independent of the user's pane-base-index setting.
//
// Every sibling at a level is created before descending into any of their
// children. tmux's own layout tree nests containers inside cells, so a
// sibling's size is a share of the *container* — not of whatever is left after
// an earlier sibling's children already subdivided it. Splitting breadth-first
// is what makes `size` mean that, and what lets a layout captured by
// `wyrm save` rebuild to the geometry it was captured from.
func applySplits(r tmux.Runner, basePane string, splits []config.Split, preWindow string, done map[string]bool, stderr io.Writer) {
	panes := make([]string, len(splits))
	current := basePane
	for i, s := range splits {
		pane := current
		if s.Type != "" {
			newPane, err := splitPane(r, current, s)
			if err != nil {
				warnf(stderr, "failed to split pane: %v", err)
				continue // panes[i] stays "": skipped below
			}
			pane = newPane
		}
		panes[i] = pane
		current = pane
	}

	// A first entry with a type splits basePane and lands its command in the
	// *new* pane, so the loop below never touches basePane — but it is still a
	// pane of this window, so pre_window still applies to it. At nested levels
	// basePane is the parent's pane, already in done, so this is a no-op there.
	sendPreWindow(r, basePane, preWindow, done, stderr)

	for i, s := range splits {
		pane := panes[i]
		if pane == "" {
			continue
		}
		sendPreWindow(r, pane, preWindow, done, stderr)
		sendKeys(r, pane, s.Command, stderr)
		applySplits(r, pane, s.Children, preWindow, done, stderr)
	}
}

func splitPane(r tmux.Runner, target string, s config.Split) (string, error) {
	dir := "-v"
	if t := strings.ToLower(s.Type); t == "h" || t == "horizontal" {
		dir = "-h"
	}
	// -d leaves the active pane where it was, so a finished window is focused
	// on its first pane rather than on whichever split happened to be created
	// last. startup_pane still overrides this.
	args := []string{"split-window", "-d", "-t", target, dir, "-P", "-F", "#{pane_id}"}
	if s.Size > 0 {
		// -l N% rather than -p N: -p was deprecated in tmux 3.1 and removed
		// from newer builds; -l with a percentage works on 3.1+.
		args = append(args, "-l", fmt.Sprintf("%d%%", s.Size))
	}
	out, err := r.Run(args...)
	if err != nil {
		return "", fmt.Errorf("%v (%s)", err, out)
	}
	if err := tmux.CheckID(tmux.PaneSigil, "pane", out); err != nil {
		return "", err
	}
	return out, nil
}

// applyPanes implements the legacy flat pane list: panes split alternately
// h/v off the previously created pane, then a layout evens them out.
func applyPanes(r tmux.Runner, windowID, initialPane string, w config.Window, done map[string]bool, stderr io.Writer) {
	sendPreWindow(r, initialPane, w.PreWindow, done, stderr)
	sendKeys(r, initialPane, w.Panes[0].Command, stderr)

	current := initialPane
	for i, p := range w.Panes[1:] {
		dir := "-h"
		if i%2 == 1 {
			dir = "-v"
		}
		out, err := r.Run("split-window", "-d", "-t", current, dir, "-P", "-F", "#{pane_id}")
		if err != nil {
			warnf(stderr, "failed to split pane: %v (%s)", err, out)
			continue
		}
		if err := tmux.CheckID(tmux.PaneSigil, "pane", out); err != nil {
			warnf(stderr, "failed to split pane: %v", err)
			continue
		}
		current = out
		sendPreWindow(r, current, w.PreWindow, done, stderr)
		sendKeys(r, current, p.Command, stderr)
	}

	layout := w.Layout
	if layout == "" && len(w.Panes) > 1 {
		layout = "tiled"
	}
	if layout != "" {
		if out, err := r.Run("select-layout", "-t", windowID, layout); err != nil {
			warnf(stderr, "failed to apply layout %q: %v (%s)", layout, err, out)
		}
	}
}

// sendPreWindow types the window's pre_window command into a pane, at most
// once per pane. pre_window is documented as running in every pane of the
// window, and tracking panes rather than split-tree entries is what makes that
// true: the tree can visit one pane more than once (a nested container reuses
// its parent's pane as its own first entry) and can leave one unvisited (a
// first entry with a type splits the window's initial pane and lands
// everything in the new one).
func sendPreWindow(r tmux.Runner, target, preWindow string, done map[string]bool, stderr io.Writer) {
	if preWindow == "" || target == "" || done[target] {
		return
	}
	done[target] = true
	sendKeys(r, target, preWindow, stderr)
}

// sendKeys types a command into the target pane. Commands starting with "#"
// are comments and are skipped.
func sendKeys(r tmux.Runner, target, command string, stderr io.Writer) {
	if command == "" || strings.HasPrefix(command, "#") {
		return
	}
	// -l types the argument literally and "--" ends the flag list. Without
	// them tmux first looks the argument up as a key name, so a command that
	// happens to be one ("up", "space", "tab", "c-c") is sent as that key
	// instead of typed, and a command starting with "-" is taken for a flag.
	// Enter is then sent separately, as an actual key.
	if out, err := r.Run("send-keys", "-t", target, "-l", "--", command); err != nil {
		warnf(stderr, "failed to run %q in %s: %v (%s)", command, target, err, out)
		return
	}
	if out, err := r.Run("send-keys", "-t", target, "Enter"); err != nil {
		warnf(stderr, "failed to run %q in %s: %v (%s)", command, target, err, out)
	}
}

// selectStartup focuses the session's startup window (given by name or index)
// and, optionally, a pane within it. Both are resolved to tmux object IDs
// (@window, %pane) via list-windows/list-panes rather than assembled into a
// "session:window.pane" target string: tmux's -t syntax treats "." as the
// window.pane separator, so a window whose name contains "." would otherwise
// be misparsed — the same hazard tmux.FindSessionID avoids for session names.
// Resolving against the live window/pane list also means an unknown
// startup_window simply isn't found (and is warned about) rather than being
// smuggled into a target string.
func selectStartup(r tmux.Runner, session, window string, pane *int, stderr io.Writer) {
	windows, err := tmux.ListWindows(r, session)
	if err != nil {
		warnf(stderr, "failed to list windows for startup_window %q: %v", window, err)
		return
	}
	windowID, ok := findStartupWindow(windows, window)
	if !ok {
		warnf(stderr, "startup_window %q not found", window)
		return
	}
	if _, err := r.Run("select-window", "-t", windowID); err != nil {
		warnf(stderr, "failed to select window %q: %v", window, err)
		return
	}
	if pane == nil {
		return
	}
	panes, err := tmux.ListPanes(r, windowID)
	if err != nil {
		warnf(stderr, "failed to list panes for startup_pane in window %q: %v", window, err)
		return
	}
	paneID, ok := findStartupPane(panes, *pane)
	if !ok {
		warnf(stderr, "startup_pane %d not found in window %q", *pane, window)
		return
	}
	if _, err := r.Run("select-pane", "-t", paneID); err != nil {
		warnf(stderr, "failed to select pane %d in window %q: %v", *pane, window, err)
	}
}

// findStartupWindow resolves a startup_window value — a window name, or a
// window index written as a string — to its tmux window ID. A name match wins
// over an index match, mirroring tmux's own "session:window" resolution.
func findStartupWindow(windows []tmux.WindowInfo, spec string) (string, bool) {
	for _, w := range windows {
		if w.Name == spec {
			return w.ID, true
		}
	}
	if idx, err := strconv.Atoi(spec); err == nil {
		for _, w := range windows {
			if w.Index == idx {
				return w.ID, true
			}
		}
	}
	return "", false
}

// findStartupPane resolves a startup_pane index (honoring the user's
// pane-base-index, since it matches against tmux's reported pane_index) to its
// tmux pane ID.
func findStartupPane(panes []tmux.PaneInfo, index int) (string, bool) {
	for _, p := range panes {
		if p.Index == index {
			return p.ID, true
		}
	}
	return "", false
}

// runHook executes a lifecycle hook via the user's shell in the given
// directory. It honors $SHELL (the same shell wyrm's pane commands land in),
// falling back to sh, rather than hardcoding bash — which isn't present on
// every system and isn't necessarily the user's shell where it is.
//
// The hook's output is streamed to stderr rather than captured, and the fact
// that it's running is announced first. Hooks are routinely slow (a
// `git pull && npm install` is the documented example) and blocking with a
// blank screen and no output looks indistinguishable from a hang. stderr
// rather than stdout so hook chatter can't be confused with wyrm's own
// progress lines.
func runHook(hook, dir, label string, stderr io.Writer) error {
	if hook == "" {
		return nil
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "sh"
	}
	_, _ = fmt.Fprintf(stderr, "wyrm: running %s: %s\n", label, hook)
	cmd := exec.Command(shell, "-c", hook)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = stderr, stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

func warnf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, "wyrm: warning: "+format+"\n", args...)
}
