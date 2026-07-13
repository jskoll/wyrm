package picker

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

// stubRunner returns canned output/err for the first arg it's given and records
// the calls it received.
type stubRunner struct {
	out   string
	err   error
	calls [][]string
}

func (s *stubRunner) Run(args ...string) (string, error) {
	s.calls = append(s.calls, args)
	return s.out, s.err
}

func TestListSessionsParses(t *testing.T) {
	// Fields are id|windows|attached|activity|name; beta is the most recently
	// active, so it must sort first. "weird|name" exercises a name containing
	// the delimiter (it's the last field, so SplitN keeps it whole).
	r := &stubRunner{out: strings.Join([]string{
		"$1|3|1|1000|alpha",
		"$2|1|0|2000|beta",
		"$3|1|0|500|weird|name",
	}, "\n")}

	got, err := ListSessions(r)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d sessions, want 3", len(got))
	}
	// Ordered by activity descending: beta(2000), alpha(1000), weird|name(500).
	if got[0].Name != "beta" || got[1].Name != "alpha" || got[2].Name != "weird|name" {
		t.Fatalf("wrong order: %q, %q, %q", got[0].Name, got[1].Name, got[2].Name)
	}
	if got[1].Windows != 3 || !got[1].Attached {
		t.Errorf("alpha parsed wrong: %+v", got[1])
	}
	if got[0].Attached {
		t.Errorf("beta should be unattached: %+v", got[0])
	}
	if got[0].ID != "$2" || got[1].ID != "$1" || got[2].ID != "$3" {
		t.Errorf("wrong IDs: beta=%q alpha=%q weird|name=%q", got[0].ID, got[1].ID, got[2].ID)
	}
	// Verify the format string is actually passed to tmux.
	if len(r.calls) != 1 || r.calls[0][0] != "list-sessions" {
		t.Fatalf("unexpected tmux call: %v", r.calls)
	}
}

func TestListSessionsNoServer(t *testing.T) {
	r := &stubRunner{out: "no server running on /tmp/tmux-1000/default", err: errors.New("exit status 1")}
	got, err := ListSessions(r)
	if err != nil {
		t.Fatalf("no-server should not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %v", got)
	}
}

func TestListSessionsRealError(t *testing.T) {
	r := &stubRunner{out: "something else broke", err: errors.New("exit status 1")}
	if _, err := ListSessions(r); err == nil {
		t.Fatal("expected error for non-'no server' failure")
	}
}

func TestKillSessionByID(t *testing.T) {
	r := &stubRunner{}
	if err := KillSession(r, "$3"); err != nil {
		t.Fatalf("KillSession: %v", err)
	}
	want := []string{"kill-session", "-t", "$3"}
	if len(r.calls) != 1 || !reflect.DeepEqual(r.calls[0], want) {
		t.Fatalf("got %v, want %v", r.calls, want)
	}
}

func TestFuzzyMatch(t *testing.T) {
	tests := []struct {
		query, target string
		want          bool
	}{
		{"", "anything", true},
		{"dev", "dev-api", true},
		{"dev", "d-e-v", true},
		{"api", "dev-api", true},
		{"xyz", "dev-api", false},
		{"DEV", "dev-api", true}, // case-insensitive
		{"deva", "dev", false},   // query longer than any subsequence
	}
	for _, tt := range tests {
		_, ok := fuzzyMatch(tt.query, tt.target)
		if ok != tt.want {
			t.Errorf("fuzzyMatch(%q, %q) ok=%v, want %v", tt.query, tt.target, ok, tt.want)
		}
	}
}

func TestFuzzyMatchRanksContiguousAndBoundary(t *testing.T) {
	contig, _ := fuzzyMatch("dev", "dev-api")
	scattered, _ := fuzzyMatch("dev", "d-e-v")
	if contig <= scattered {
		t.Errorf("contiguous %d should outscore scattered %d", contig, scattered)
	}
	boundary, _ := fuzzyMatch("api", "dev-api")  // matches at word start
	midword, _ := fuzzyMatch("api", "devapisrv") // matches mid-word
	if boundary <= midword {
		t.Errorf("boundary %d should outscore mid-word %d", boundary, midword)
	}
}

func sessions(names ...string) []Session {
	s := make([]Session, len(names))
	for i, n := range names {
		s[i] = Session{Name: n, Windows: 1}
	}
	return s
}

func TestModelFilterOrdersByScore(t *testing.T) {
	m := newModel(sessions("d-e-v", "dev-api", "prod"))
	m.appendRune('d')
	m.appendRune('e')
	m.appendRune('v')
	if len(m.filtered) != 2 {
		t.Fatalf("want 2 matches, got %d: %v", len(m.filtered), names(m.filtered))
	}
	// dev-api (contiguous) should rank above d-e-v (scattered).
	if m.filtered[0].Name != "dev-api" {
		t.Errorf("best match should be dev-api, got %q", m.filtered[0].Name)
	}
}

func TestModelCursorClampsOnFilter(t *testing.T) {
	m := newModel(sessions("alpha", "beta", "gamma"))
	m.cursor = 2
	m.appendRune('l') // only "alpha" contains an 'l', shrinking the list to one
	if len(m.filtered) != 1 {
		t.Fatalf("want 1 match for 'l', got %d: %v", len(m.filtered), names(m.filtered))
	}
	if m.cursor >= len(m.filtered) {
		t.Fatalf("cursor %d out of range after filter (len %d)", m.cursor, len(m.filtered))
	}
}

func TestModelMovement(t *testing.T) {
	m := newModel(sessions("a", "b", "c"))
	m.moveUp() // already at top, no-op
	if m.cursor != 0 {
		t.Fatalf("moveUp at top should stay 0, got %d", m.cursor)
	}
	m.moveDown()
	m.moveDown()
	if m.cursor != 2 {
		t.Fatalf("want cursor 2, got %d", m.cursor)
	}
	m.moveDown() // at bottom, no-op
	if m.cursor != 2 {
		t.Fatalf("moveDown at bottom should stay 2, got %d", m.cursor)
	}
	s, ok := m.selected()
	if !ok || s.Name != "c" {
		t.Fatalf("selected = %v, %v; want c", s.Name, ok)
	}
}

func TestModelBackspace(t *testing.T) {
	m := newModel(sessions("alpha", "beta"))
	m.appendRune('x') // matches nothing
	if len(m.filtered) != 0 {
		t.Fatalf("want 0 matches for 'x', got %d", len(m.filtered))
	}
	m.backspace()
	if len(m.filtered) != 2 {
		t.Fatalf("backspace should restore all, got %d", len(m.filtered))
	}
	m.backspace() // empty query, no-op
	if m.query != "" {
		t.Fatalf("query should be empty, got %q", m.query)
	}
}

func TestViewport(t *testing.T) {
	// Fits: whole range.
	if s, e := viewport(0, 3, 10); s != 0 || e != 3 {
		t.Errorf("fits: got [%d,%d), want [0,3)", s, e)
	}
	// Cursor past the window scrolls it down.
	if s, e := viewport(5, 10, 3); s != 3 || e != 6 {
		t.Errorf("scroll: got [%d,%d), want [3,6)", s, e)
	}
	// Cursor at end clamps end to n.
	if s, e := viewport(9, 10, 3); s != 7 || e != 10 {
		t.Errorf("end: got [%d,%d), want [7,10)", s, e)
	}
}

// withNoColor unsets NO_COLOR for the duration of the test, restoring
// whatever was there before. testing.T.Setenv can't represent "unset", which
// is the state colorize needs to treat as "color enabled".
func withNoColor(t *testing.T, set bool) {
	t.Helper()
	if set {
		t.Setenv("NO_COLOR", "1")
		return
	}
	orig, had := os.LookupEnv("NO_COLOR")
	if err := os.Unsetenv("NO_COLOR"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if had {
			os.Setenv("NO_COLOR", orig) //nolint:errcheck
		}
	})
}

func TestFormatRowColor(t *testing.T) {
	withNoColor(t, false)
	got := formatRow(Session{Name: "alpha", Windows: 2, Attached: true})
	if !strings.Contains(got, cyan) || !strings.Contains(got, green) {
		t.Errorf("formatRow = %q, want cyan/green color codes present", got)
	}
	if !strings.Contains(got, "2 windows") || !strings.Contains(got, "(attached)") {
		t.Errorf("formatRow = %q, want plain text preserved alongside color codes", got)
	}
}

func TestFormatRowNoColor(t *testing.T) {
	withNoColor(t, true)
	got := formatRow(Session{Name: "alpha", Windows: 1, Attached: true})
	if strings.Contains(got, esc) {
		t.Errorf("formatRow with NO_COLOR set = %q, want no ANSI escape codes", got)
	}
	if !strings.Contains(got, "1 window") || !strings.Contains(got, "(attached)") {
		t.Errorf("formatRow = %q, want plain text present", got)
	}
}

func names(s []Session) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[i] = v.Name
	}
	return out
}
