package tui

import (
	"strings"
	"testing"

	"github.com/jskoll/wyrm/internal/sessions"
)

func filterModel() Model {
	m := New(nopRunner(), nil)
	m.width, m.height = 100, 40
	m.ready = true
	m.sessions = []sessions.Session{
		{ID: "$1", Name: "api-server", Windows: 2},
		{ID: "$2", Name: "web-frontend", Windows: 1},
		{ID: "$3", Name: "api-worker", Windows: 3},
	}
	m.focus = panelSessions
	return m
}

func TestFilterNarrowsFocusedPanel(t *testing.T) {
	m := filterModel()
	m, _ = update(m, key("/"))
	if m.mode != modeFilter {
		t.Fatalf("mode = %v, want modeFilter after /", m.mode)
	}
	for _, r := range "api" {
		m, _ = update(m, key(string(r)))
	}
	visible := m.visibleSessions()
	if len(visible) != 2 {
		t.Fatalf("visible sessions = %+v, want the two api-* entries", visible)
	}
	for _, s := range visible {
		if !strings.HasPrefix(s.Name, "api") {
			t.Errorf("session %q does not match the filter", s.Name)
		}
	}
}

// TestFilterOnlyAppliesToFocusedPanel: narrowing every panel at once would
// hide the session a window belongs to, which is the context the cascade
// exists to show.
func TestFilterOnlyAppliesToFocusedPanel(t *testing.T) {
	m := filterModel()
	m.filter = "api"
	m.focus = panelProjects
	if got := len(m.visibleSessions()); got != 3 {
		t.Errorf("visible sessions = %d, want all 3 while another panel has focus", got)
	}
}

// TestFilterClampsSelection: narrowing the list can leave the cursor past its
// end, and the selection has to keep meaning what's under it.
func TestFilterClampsSelection(t *testing.T) {
	m := filterModel()
	m.cur[panelSessions] = 2
	m, _ = update(m, key("/"))
	for _, r := range "web" {
		m, _ = update(m, key(string(r)))
	}
	if m.cur[panelSessions] >= len(m.visibleSessions()) {
		t.Fatalf("sessionCur = %d, out of range for %d visible", m.cur[panelSessions], len(m.visibleSessions()))
	}
	s, ok := m.currentSession()
	if !ok || s.Name != "web-frontend" {
		t.Errorf("currentSession = %+v, want web-frontend", s)
	}
}

func TestEscClearsFilter(t *testing.T) {
	m := filterModel()
	m.filter = "api"
	m, _ = update(m, key("esc"))
	if m.filter != "" {
		t.Errorf("filter = %q, want cleared by esc", m.filter)
	}
	if got := len(m.visibleSessions()); got != 3 {
		t.Errorf("visible sessions = %d, want all 3 after clearing", got)
	}
}

func TestFilterShownInFooter(t *testing.T) {
	m := filterModel()
	m, _ = update(m, key("/"))
	m, _ = update(m, key("a"))
	if view := m.View(); !strings.Contains(view, "/a") {
		t.Errorf("view does not show the active filter:\n%s", view)
	}
}

// TestGAndGJumpToEnds covers the navigation the help overlay already had but
// the lists didn't.
func TestGAndGJumpToEnds(t *testing.T) {
	m := filterModel()
	m, _ = update(m, key("G"))
	if m.cur[panelSessions] != 2 {
		t.Errorf("sessionCur = %d after G, want the last entry (2)", m.cur[panelSessions])
	}
	m, _ = update(m, key("g"))
	if m.cur[panelSessions] != 0 {
		t.Errorf("sessionCur = %d after g, want the first entry (0)", m.cur[panelSessions])
	}
}

// TestMoveCursorClampsRatherThanRefusing: a PgDn near the bottom should land
// on the last row, not do nothing.
func TestMoveCursorClampsRatherThanRefusing(t *testing.T) {
	m := filterModel()
	m.cur[panelSessions] = 1
	m, _ = update(m, key("pgdown"))
	if m.cur[panelSessions] != 2 {
		t.Errorf("sessionCur = %d after pgdown, want it clamped to the last entry", m.cur[panelSessions])
	}
}
