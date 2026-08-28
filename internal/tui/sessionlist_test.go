package tui

import (
	"strings"
	"testing"

	"github.com/jskoll/wyrm/internal/sessions"
)

// model with two running sessions and three known projects, one of which
// ("api") is the running session's own config.
func allSessionsModel() Model {
	m := New(nopRunner(), nil)
	m.sessions = []sessions.Session{
		{ID: "$1", Name: "api", Windows: 2, Attached: true},
		{ID: "$2", Name: "adhoc", Windows: 1},
	}
	m.projects = []Project{
		{Name: "api", Path: "/cfg/api.wyrm.toml", Running: true, SessionID: "$1"},
		{Name: "dragon-cli", Path: "/cfg/dragon-cli.wyrm.toml"},
		{Name: "notes", Root: "/home/notes", Zoxide: true},
	}
	return m
}

func entryNames(entries []sessionEntry) []string {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name
	}
	return names
}

func TestSessionEntriesDefaultsToRunningOnly(t *testing.T) {
	m := allSessionsModel()
	got := entryNames(m.sessionEntries())
	want := []string{"api", "adhoc"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("sessionEntries() = %v, want %v", got, want)
	}
	for _, e := range m.sessionEntries() {
		if !e.Running {
			t.Errorf("entry %q Running = false, want true with allSessions off", e.Name)
		}
	}
}

func TestSessionEntriesAllAppendsStoppedProjects(t *testing.T) {
	m := allSessionsModel()
	m.allSessions = true

	entries := m.sessionEntries()
	got := entryNames(entries)
	want := []string{"api", "adhoc", "dragon-cli", "notes"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("sessionEntries() = %v, want %v (running first, stopped after)", got, want)
	}
	// The running row that has a config keeps it; the one started outside
	// wyrm has none.
	if !entries[0].Running || !entries[0].HasProject {
		t.Errorf("api entry = %+v, want running with a project", entries[0])
	}
	if !entries[1].Running || entries[1].HasProject {
		t.Errorf("adhoc entry = %+v, want running with no project", entries[1])
	}
	for _, e := range entries[2:] {
		if e.Running || !e.HasProject {
			t.Errorf("entry %q = %+v, want stopped with a project", e.Name, e)
		}
	}
}

// A project annotated Running but absent from the session list must not be
// listed twice or as stopped — the two lists arrive as separate messages, so
// they disagree for a frame after every start and kill.
func TestSessionEntriesTrustsSessionListOverProjectAnnotation(t *testing.T) {
	m := allSessionsModel()
	m.allSessions = true
	m.projects = append(m.projects, Project{Name: "adhoc", Path: "/cfg/adhoc.wyrm.toml"})

	got := entryNames(m.sessionEntries())
	want := []string{"api", "adhoc", "dragon-cli", "notes"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("sessionEntries() = %v, want %v (no duplicate adhoc row)", got, want)
	}
}

func TestCurrentSessionReportsFalseOnStoppedRow(t *testing.T) {
	m := allSessionsModel()
	m.allSessions = true
	m.cur[panelSessions] = 2 // dragon-cli, stopped

	if s, ok := m.currentSession(); ok {
		t.Errorf("currentSession() on a stopped row = %+v, true; want false so session actions no-op", s)
	}
	e, ok := m.currentSessionEntry()
	if !ok || e.Name != "dragon-cli" {
		t.Fatalf("currentSessionEntry() = %+v, %v; want the dragon-cli row", e, ok)
	}
	// Everything that targets a session ID must decline rather than act.
	if _, _, ok := killSession(m); ok {
		t.Error("killSession on a stopped row = ok, want false")
	}
	if _, cmd := m.startRename(); cmd != nil {
		t.Error("startRename on a stopped row returned a command, want none")
	}
	if _, cmd := m.startNewWindow(); cmd != nil {
		t.Error("startNewWindow on a stopped row returned a command, want none")
	}
}

func TestToggleAllSessionsKeyOnlyOnSessionsPanel(t *testing.T) {
	m := allSessionsModel()
	m.focus = panelProjects
	m, _ = update(m, key("a"))
	if m.allSessions {
		t.Error(`"a" on the Projects panel toggled allSessions, want ignored`)
	}

	m.focus = panelSessions
	m, _ = update(m, key("a"))
	if !m.allSessions {
		t.Fatal(`"a" on the Sessions panel did not turn allSessions on`)
	}
	if got := m.panelLen(panelSessions); got != 4 {
		t.Errorf("Sessions panel length after toggle = %d, want 4", got)
	}
	m, _ = update(m, key("a"))
	if m.allSessions {
		t.Error(`second "a" did not turn allSessions back off`)
	}
}

// The cursor can sit past the end of the shorter list when "all" is turned
// off, so the toggle has to clamp it.
func TestToggleAllSessionsOffClampsCursor(t *testing.T) {
	m := allSessionsModel()
	m.focus = panelSessions
	m.allSessions = true
	m.cur[panelSessions] = 3 // notes, only present while "all" is on

	m, _ = update(m, key("a"))
	if m.cur[panelSessions] >= len(m.sessionEntries()) {
		t.Errorf("cursor = %d after toggling off, want < %d", m.cur[panelSessions], len(m.sessionEntries()))
	}
}

func TestEnterStartsStoppedSessionRow(t *testing.T) {
	var started []string
	m := allSessionsModel()
	m.runner = funcRunner{fn: func(args ...string) (string, error) {
		started = append(started, strings.Join(args, " "))
		return "", nil
	}}
	m.allSessions = true
	m.focus = panelSessions
	m.cur[panelSessions] = 2 // dragon-cli, stopped

	_, cmd := update(m, key("enter"))
	if cmd == nil {
		t.Fatal("enter on a stopped row returned no command, want a start")
	}
	// The command loads the config from disk and fails there in a test, which
	// is fine: what matters is that it is a start attempt, not an attach.
	if msg, ok := run(cmd).(projectStartedMsg); !ok {
		t.Errorf("enter on a stopped row produced %T, want projectStartedMsg", msg)
	}
}

func TestEnterAttachesRunningSessionRow(t *testing.T) {
	m := allSessionsModel()
	m.allSessions = true
	m.focus = panelSessions
	m.cur[panelSessions] = 0 // api, running

	next, _ := update(m, key("enter"))
	if next.pendingAttach != "$1" {
		t.Errorf("pendingAttach = %q, want $1 — a running row still attaches", next.pendingAttach)
	}
}

func TestStoppedRowRendersAsStopped(t *testing.T) {
	m := allSessionsModel()
	m.allSessions = true

	rows := sessionRows(m)
	if len(rows) != 4 {
		t.Fatalf("sessionRows = %d rows, want 4", len(rows))
	}
	text := func(r []span) string {
		var b strings.Builder
		for _, s := range r {
			b.WriteString(s.text)
		}
		return b.String()
	}
	if got := text(rows[2]); !strings.Contains(got, "dragon-cli") || !strings.Contains(got, "stopped") {
		t.Errorf("stopped row = %q, want the name and \"stopped\"", got)
	}
	if got := text(rows[0]); !strings.Contains(got, "(2w)") {
		t.Errorf("running row = %q, want its window count", got)
	}
}

func TestSessionMenuOffersStartOnStoppedRow(t *testing.T) {
	m := allSessionsModel()
	m.allSessions = true
	m.cur[panelSessions] = 2

	entries := sessionMenu(m)
	if len(entries) != 1 || entries[0].op != menuStart {
		t.Fatalf("sessionMenu on a stopped row = %+v, want a single Start entry", entries)
	}

	m.cur[panelSessions] = 0
	entries = sessionMenu(m)
	for _, e := range entries {
		if e.op == menuStart {
			t.Error("sessionMenu on a running row offers Start, want attach/rename/kill only")
		}
	}
}

// The filter indexes the same list the cursor does, so stopped rows have to be
// filterable by name like any other.
func TestFilterMatchesStoppedRows(t *testing.T) {
	m := allSessionsModel()
	m.allSessions = true
	m.focus = panelSessions
	m.filter = "dragon"

	got := entryNames(m.visibleSessions())
	if len(got) != 1 || got[0] != "dragon-cli" {
		t.Errorf("visibleSessions() with filter %q = %v, want [dragon-cli]", m.filter, got)
	}
}
