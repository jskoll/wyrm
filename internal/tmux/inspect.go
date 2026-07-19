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
		panes = append(panes, PaneInfo{
			ID:      parts[0],
			Index:   index,
			Active:  parts[2] == "1",
			Command: parts[3],
		})
	}
	return panes, nil
}
