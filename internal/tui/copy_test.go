package tui

import (
	"strings"
	"testing"

	"github.com/jskoll/wyrm/internal/sessions"
	"github.com/jskoll/wyrm/internal/tmux"
)

func TestCopyKeyInPanels(t *testing.T) {
	m := New(nopRunner(), nil)
	m.width, m.height, m.ready = 100, 30, true
	m.sessions = []sessions.Session{{ID: "$1", Name: "my-session"}}
	m.cur[panelSessions] = 0
	m.focus = panelSessions

	m, _ = update(m, key("y"))
	if !strings.Contains(m.info, "my-session") {
		t.Errorf("expected info message mentioning my-session, got %q", m.info)
	}

	// Any key clears info message
	m, _ = update(m, key("j"))
	if m.info != "" {
		t.Errorf("info should be cleared on keypress, got %q", m.info)
	}

	// Copy project path
	m.projects = []Project{{Name: "proj", Path: "/path/to/proj"}}
	m.cur[panelProjects] = 0
	m.focus = panelProjects
	m, _ = update(m, key("y"))
	if !strings.Contains(m.info, "/path/to/proj") {
		t.Errorf("expected info mentioning /path/to/proj, got %q", m.info)
	}

	// Copy window name
	m.windows = []tmux.WindowInfo{{ID: "@1", Name: "code"}}
	m.cur[panelWindows] = 0
	m.focus = panelWindows
	m, _ = update(m, key("y"))
	if !strings.Contains(m.info, "code") {
		t.Errorf("expected info mentioning code, got %q", m.info)
	}
}
