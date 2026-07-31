package tmux

import (
	"fmt"
	"strconv"
	"strings"
)

// WindowInfo is one window of a running session, as reported by tmux.
type WindowInfo struct {
	Index  int
	ID     string
	Active bool
	Layout string // #{window_layout}: the split tree, see internal/freeze
	Name   string
}

// PaneInfo is one pane of a running window, as reported by tmux.
type PaneInfo struct {
	ID      string
	Index   int
	Active  bool
	Command string // #{pane_current_command}: the pane's foreground process
}

// SessionPath returns a session's working directory (#{session_path}) — the
// directory tmux opens new windows in, and the closest thing tmux records to
// "where this project lives".
func SessionPath(r Runner, sessionID string) (string, error) {
	out, err := r.Run("display-message", "-p", "-t", sessionID, "-F", "#{session_path}")
	if err != nil {
		return "", fmt.Errorf("reading session path: %v (%s)", err, out)
	}
	return strings.TrimSpace(out), nil
}

const windowListFormat = "#{window_index}|#{window_id}|#{?window_active,1,0}|#{window_layout}|#{window_name}"

// ListWindows returns the windows of sessionID (a tmux session ID such as
// "$3"), in tmux's own window-index order.
func ListWindows(r Runner, sessionID string) ([]WindowInfo, error) {
	out, err := r.Run("list-windows", "-t", sessionID, "-F", windowListFormat)
	if err != nil {
		return nil, fmt.Errorf("listing windows: %v (%s)", err, out)
	}
	var windows []WindowInfo
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 5)
		if len(parts) != 5 {
			return nil, fmt.Errorf("unexpected list-windows output %q", line)
		}
		index, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("unexpected window index %q", parts[0])
		}
		if err := CheckID(WindowSigil, "window", parts[1]); err != nil {
			return nil, fmt.Errorf("listing windows: %w", err)
		}
		windows = append(windows, WindowInfo{
			Index:  index,
			ID:     parts[1],
			Active: parts[2] == "1",
			Layout: parts[3],
			Name:   parts[4],
		})
	}
	return windows, nil
}

// PaneRef locates one pane within the whole tmux server, alongside the command
// it's running. It's what a server-wide `list-panes -a` yields: enough to decide
// which panes are worth inspecting, and which window and session to attribute
// the result to.
type PaneRef struct {
	SessionID string
	WindowID  string
	PaneID    string
	Command   string
}

const allPanesFormat = "#{session_id}|#{window_id}|#{pane_id}|#{pane_current_command}"

// ListAllPanes returns every pane on the tmux server. The TUI uses it to find
// agent panes across all sessions in one round trip — the alternative, walking
// list-windows and list-panes per session, costs a tmux call per window and is
// run on a timer.
//
// Like picker.ListSessions, no server running is not an error: it just means
// there are no panes.
func ListAllPanes(r Runner) ([]PaneRef, error) {
	out, err := r.Run("list-panes", "-a", "-F", allPanesFormat)
	if err != nil {
		if NoServerRunning(out) {
			return nil, nil
		}
		return nil, fmt.Errorf("listing panes: %v (%s)", err, out)
	}
	var refs []PaneRef
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) != 4 {
			return nil, fmt.Errorf("unexpected list-panes output %q", line)
		}
		refs = append(refs, PaneRef{
			SessionID: parts[0],
			WindowID:  parts[1],
			PaneID:    parts[2],
			Command:   parts[3],
		})
	}
	return refs, nil
}

const paneListFormat = "#{pane_id}|#{pane_index}|#{?pane_active,1,0}|#{pane_current_command}"

// ListPanes returns the panes of target (a window ID such as "@2", or a
// session ID to list every pane in the session).
func ListPanes(r Runner, target string) ([]PaneInfo, error) {
	out, err := r.Run("list-panes", "-t", target, "-F", paneListFormat)
	if err != nil {
		return nil, fmt.Errorf("listing panes: %v (%s)", err, out)
	}
	var panes []PaneInfo
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) != 4 {
			return nil, fmt.Errorf("unexpected list-panes output %q", line)
		}
		index, err := strconv.Atoi(parts[1])
		if err != nil {
			return nil, fmt.Errorf("unexpected pane index %q", parts[1])
		}
		if err := CheckID(PaneSigil, "pane", parts[0]); err != nil {
			return nil, fmt.Errorf("listing panes: %w", err)
		}
		panes = append(panes, PaneInfo{
			ID:      parts[0],
			Index:   index,
			Active:  parts[2] == "1",
			Command: parts[3],
		})
	}
	return panes, nil
}
