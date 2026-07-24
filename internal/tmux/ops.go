package tmux

import (
	"fmt"
	"strings"
)

// CapturePane returns the visible contents of a pane as plain text. target is a
// pane ID (e.g. "%1"); the -p flag prints to stdout instead of a paste buffer.
// The wyrm TUI polls this to show a live preview of the selected pane.
func CapturePane(r Runner, target string) (string, error) {
	out, err := r.Run("capture-pane", "-p", "-t", target)
	if err != nil {
		return "", fmt.Errorf("capturing pane %q: %v (%s)", target, err, out)
	}
	return out, nil
}

// KillWindow removes a window by its window ID (e.g. "@2").
func KillWindow(r Runner, windowID string) error {
	if out, err := r.Run("kill-window", "-t", windowID); err != nil {
		return fmt.Errorf("killing window %q: %v (%s)", windowID, err, out)
	}
	return nil
}

// KillPane removes a pane by its pane ID (e.g. "%3").
func KillPane(r Runner, paneID string) error {
	if out, err := r.Run("kill-pane", "-t", paneID); err != nil {
		return fmt.Errorf("killing pane %q: %v (%s)", paneID, err, out)
	}
	return nil
}

// RenameSession renames the session with the given ID (e.g. "$1"). Targeting by
// ID rather than name is required so a name containing "." isn't misparsed —
// see FindSessionID.
func RenameSession(r Runner, sessionID, name string) error {
	if out, err := r.Run("rename-session", "-t", sessionID, name); err != nil {
		return fmt.Errorf("renaming session %q: %v (%s)", sessionID, err, out)
	}
	return nil
}

// RenameWindow renames the window with the given ID (e.g. "@2").
func RenameWindow(r Runner, windowID, name string) error {
	if out, err := r.Run("rename-window", "-t", windowID, name); err != nil {
		return fmt.Errorf("renaming window %q: %v (%s)", windowID, err, out)
	}
	return nil
}

// NewWindow creates a window in sessionID and returns the new window and pane
// IDs. name and root are optional: an empty name lets tmux auto-name the window,
// and an empty root leaves the working directory to tmux's default.
func NewWindow(r Runner, sessionID, name, root string) (windowID, paneID string, err error) {
	args := []string{"new-window", "-P", "-F", "#{window_id}|#{pane_id}", "-t", sessionID}
	if name != "" {
		args = append(args, "-n", name)
	}
	if root != "" {
		args = append(args, "-c", root)
	}
	out, err := r.Run(args...)
	if err != nil {
		return "", "", fmt.Errorf("creating window in %q: %v (%s)", sessionID, err, out)
	}
	win, pane, ok := strings.Cut(strings.TrimRight(out, "\r"), "|")
	if !ok {
		return "", "", fmt.Errorf("unexpected new-window output %q", out)
	}
	return win, pane, nil
}

// SelectLayout applies a named tmux layout (e.g. "tiled", "even-horizontal") to
// the window with the given ID.
func SelectLayout(r Runner, windowID, layout string) error {
	if out, err := r.Run("select-layout", "-t", windowID, layout); err != nil {
		return fmt.Errorf("applying layout %q to %q: %v (%s)", layout, windowID, err, out)
	}
	return nil
}

// SelectWindow makes windowID the active window of its session, so a subsequent
// attach lands there.
func SelectWindow(r Runner, windowID string) error {
	if out, err := r.Run("select-window", "-t", windowID); err != nil {
		return fmt.Errorf("selecting window %q: %v (%s)", windowID, err, out)
	}
	return nil
}

// SelectPane makes paneID the active pane of its window.
func SelectPane(r Runner, paneID string) error {
	if out, err := r.Run("select-pane", "-t", paneID); err != nil {
		return fmt.Errorf("selecting pane %q: %v (%s)", paneID, err, out)
	}
	return nil
}

// ZoomPane toggles the zoomed state of a pane (resize-pane -Z).
func ZoomPane(r Runner, paneID string) error {
	if out, err := r.Run("resize-pane", "-Z", "-t", paneID); err != nil {
		return fmt.Errorf("zooming pane %q: %v (%s)", paneID, err, out)
	}
	return nil
}
