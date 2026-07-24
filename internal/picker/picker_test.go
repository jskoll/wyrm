package picker

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/jskoll/wyrm/internal/tmux"
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

// scriptedRunner returns canned output for each tmux subcommand (args[0]),
// unlike stubRunner's single fixed response — needed for runLoop tests that
// drive more than one distinct tmux call in the same run (e.g. list-windows
// during Ctrl-W, then list-sessions again after Ctrl-X).
type scriptedRunner struct {
	out   map[string]string
	calls [][]string
}

func (s *scriptedRunner) Run(args ...string) (string, error) {
	s.calls = append(s.calls, args)
	return s.out[args[0]], nil
}

func (s *scriptedRunner) called(name string, target string) bool {
	for _, c := range s.calls {
		if len(c) >= 3 && c[0] == name && c[2] == target {
			return true
		}
	}
	return false
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

func TestModelEnterExitWindows(t *testing.T) {
	m := newModel(sessions("alpha", "beta"))
	m.cursor = 1 // "beta"
	m.query = "b"
	m.filter()

	s, ok := m.selected()
	if !ok || s.Name != "beta" {
		t.Fatalf("selected = %v, %v; want beta", s.Name, ok)
	}

	windows := []tmux.WindowInfo{{ID: "@1", Name: "editor"}, {ID: "@2", Name: "server", Active: true}}
	m.enterWindows(s, windows)
	if !m.viewingWindows {
		t.Fatal("viewingWindows = false after enterWindows")
	}
	if m.windowSession.Name != "beta" || len(m.windows) != 2 || m.windowCursor != 0 {
		t.Fatalf("state after enterWindows = %+v", m)
	}

	m.exitWindows()
	if m.viewingWindows || m.windows != nil || m.windowSession != (Session{}) {
		t.Fatalf("state after exitWindows = %+v, want cleared", m)
	}
	// The session list itself (query, cursor) is untouched by the round trip.
	if m.query != "b" || m.cursor != 0 {
		t.Fatalf("session state after exitWindows = query %q cursor %d, want unchanged", m.query, m.cursor)
	}
}

func TestModelWindowMovement(t *testing.T) {
	m := &model{}
	m.enterWindows(Session{Name: "beta"}, []tmux.WindowInfo{
		{ID: "@1", Name: "a"}, {ID: "@2", Name: "b"}, {ID: "@3", Name: "c"},
	})

	m.moveWindowUp() // already at top, no-op
	if m.windowCursor != 0 {
		t.Fatalf("moveWindowUp at top should stay 0, got %d", m.windowCursor)
	}
	m.moveWindowDown()
	m.moveWindowDown()
	if m.windowCursor != 2 {
		t.Fatalf("want windowCursor 2, got %d", m.windowCursor)
	}
	m.moveWindowDown() // at bottom, no-op
	if m.windowCursor != 2 {
		t.Fatalf("moveWindowDown at bottom should stay 2, got %d", m.windowCursor)
	}
	w, ok := m.selectedWindow()
	if !ok || w.Name != "c" {
		t.Fatalf("selectedWindow = %v, %v; want c", w.Name, ok)
	}
}

func TestModelSelectedWindowEmpty(t *testing.T) {
	m := &model{}
	if _, ok := m.selectedWindow(); ok {
		t.Error("selectedWindow on an empty model: want not-ok")
	}
}

func TestSelectWindow(t *testing.T) {
	r := &stubRunner{}
	if err := selectWindow(r, "@3"); err != nil {
		t.Fatalf("selectWindow: %v", err)
	}
	want := []string{"select-window", "-t", "@3"}
	if len(r.calls) != 1 || !reflect.DeepEqual(r.calls[0], want) {
		t.Fatalf("got %v, want %v", r.calls, want)
	}
}

func TestSelectWindowError(t *testing.T) {
	r := &stubRunner{err: errors.New("exit status 1")}
	if err := selectWindow(r, "@3"); err == nil {
		t.Error("selectWindow with a failing command: want error, got nil")
	}
}

func TestFormatWindowRowColor(t *testing.T) {
	withNoColor(t, false)
	got := formatWindowRow(tmux.WindowInfo{Name: "editor", Active: true})
	if !strings.Contains(got, green) {
		t.Errorf("formatWindowRow = %q, want the green color code present", got)
	}
	if !strings.Contains(got, "editor") || !strings.Contains(got, "(active)") {
		t.Errorf("formatWindowRow = %q, want plain text preserved alongside color codes", got)
	}
}

func TestFormatWindowRowInactiveNoMarker(t *testing.T) {
	got := formatWindowRow(tmux.WindowInfo{Name: "editor"})
	if got != "editor" {
		t.Errorf("formatWindowRow(inactive) = %q, want just the name", got)
	}
}

// fixedHeight is a runLoop height func that reports a constant terminal
// size, comfortably above the fallback threshold used by every test session
// list here.
func fixedHeight() int { return 20 }

func TestRunLoopFilterAndSelect(t *testing.T) {
	sessions := []Session{
		{ID: "$1", Name: "alpha"},
		{ID: "$2", Name: "beta"},
	}
	br := bufio.NewReader(strings.NewReader("be\r")) // type "be", Enter
	var out bytes.Buffer
	id, err := runLoop(&stubRunner{}, sessions, br, &renderer{w: &out}, fixedHeight)
	if err != nil {
		t.Fatalf("runLoop: %v", err)
	}
	if id != "$2" {
		t.Errorf("runLoop = %q, want $2 (beta, the only match for %q)", id, "be")
	}
	if !strings.Contains(out.String(), "beta") {
		t.Errorf("drawn output = %q, want the beta row", out.String())
	}
}

func TestRunLoopAbortAtTopLevelReturnsEmpty(t *testing.T) {
	sessions := []Session{{ID: "$1", Name: "alpha"}}
	br := bufio.NewReader(strings.NewReader("\x1b")) // lone Escape
	id, err := runLoop(&stubRunner{}, sessions, br, &renderer{w: &bytes.Buffer{}}, fixedHeight)
	if err != nil || id != "" {
		t.Errorf("runLoop(Esc) = %q, %v; want empty, nil", id, err)
	}
}

func TestRunLoopQuitReturnsEmpty(t *testing.T) {
	sessions := []Session{{ID: "$1", Name: "alpha"}}
	br := bufio.NewReader(strings.NewReader("\x03")) // Ctrl-C
	id, err := runLoop(&stubRunner{}, sessions, br, &renderer{w: &bytes.Buffer{}}, fixedHeight)
	if err != nil || id != "" {
		t.Errorf("runLoop(Ctrl-C) = %q, %v; want empty, nil", id, err)
	}
}

func TestRunLoopWindowsDrilldownSelectsWindow(t *testing.T) {
	sessions := []Session{{ID: "$1", Name: "alpha"}}
	r := &scriptedRunner{out: map[string]string{
		"list-windows": "0|@1|1|abcd,80x24,0,0,0|code\n1|@2|0|abcd,80x24,0,0,1|logs",
	}}
	br := bufio.NewReader(strings.NewReader("\x17\r")) // Ctrl-W, Enter (first window: code)
	id, err := runLoop(r, sessions, br, &renderer{w: &bytes.Buffer{}}, fixedHeight)
	if err != nil {
		t.Fatalf("runLoop: %v", err)
	}
	if id != "$1" {
		t.Errorf("runLoop = %q, want $1 (the session, once a window is chosen)", id)
	}
	if !r.called("select-window", "@1") {
		t.Errorf("select-window @1 not called: %v", r.calls)
	}
}

// stepReader hands back its data one byte per Read call, mimicking how
// bufio.Reader sees real keystrokes arriving from a tty: each one a separate
// short read. A strings.Reader would instead let bufio buffer the whole
// input in one fill, so two consecutive Escape bytes would misparse as one
// (invalid) escape sequence instead of two lone Escapes — see readKey's
// br.Buffered() check.
type stepReader struct{ data []byte }

func (s *stepReader) Read(p []byte) (int, error) {
	if len(s.data) == 0 {
		return 0, io.EOF
	}
	p[0] = s.data[0]
	s.data = s.data[1:]
	return 1, nil
}

func TestRunLoopEscBacksOutOfWindowsThenQuits(t *testing.T) {
	sessions := []Session{{ID: "$1", Name: "alpha"}}
	r := &scriptedRunner{out: map[string]string{
		"list-windows": "0|@1|1|abcd,80x24,0,0,0|code",
	}}
	// Ctrl-W into the window list, Esc back to the session list, Esc to quit.
	br := bufio.NewReader(&stepReader{data: []byte("\x17\x1b\x1b")})
	id, err := runLoop(r, sessions, br, &renderer{w: &bytes.Buffer{}}, fixedHeight)
	if err != nil || id != "" {
		t.Errorf("runLoop = %q, %v; want empty, nil", id, err)
	}
}

func TestRunLoopKillSession(t *testing.T) {
	sessions := []Session{
		{ID: "$1", Name: "alpha"},
		{ID: "$2", Name: "beta"},
	}
	r := &scriptedRunner{out: map[string]string{
		"list-sessions": "$2|1|0|1000|beta",
	}}
	// Ctrl-X kills the selected (first, alpha) session, then Esc quits.
	br := bufio.NewReader(strings.NewReader("\x18\x1b"))
	id, err := runLoop(r, sessions, br, &renderer{w: &bytes.Buffer{}}, fixedHeight)
	if err != nil || id != "" {
		t.Errorf("runLoop = %q, %v; want empty, nil", id, err)
	}
	if !r.called("kill-session", "$1") {
		t.Errorf("kill-session $1 not called: %v", r.calls)
	}
}

// TestRunLoopReservesBottomRow guards the fix for the popup-jitter bug: when
// the session list overflows the viewport, the drawn frame must stay within
// h-1 lines. Drawing exactly h lines — each terminated with "\r\n" — scrolls
// the terminal on the final newline every keypress, which reads as jitter,
// especially inside a tmux display-popup.
func TestRunLoopReservesBottomRow(t *testing.T) {
	// Far more sessions than fixedHeight's 20 rows can show, forcing the
	// viewport to clamp.
	var sessions []Session
	for i := 0; i < 50; i++ {
		sessions = append(sessions, Session{ID: "$1", Name: "session"})
	}
	rn := &renderer{w: &bytes.Buffer{}}
	// Ctrl-C quits right after the first frame is drawn.
	br := bufio.NewReader(strings.NewReader("\x03"))
	if _, err := runLoop(&stubRunner{}, sessions, br, rn, fixedHeight); err != nil {
		t.Fatalf("runLoop: %v", err)
	}
	if h := fixedHeight(); rn.prevLines > h-1 {
		t.Errorf("frame drew %d lines in a %d-row terminal; want <= %d to avoid a bottom-row scroll", rn.prevLines, h, h-1)
	}
}

func TestRendererEnterAndClear(t *testing.T) {
	var buf bytes.Buffer
	rn := &renderer{w: &buf}

	// enter hides the cursor and disables autowrap so long rows are clipped
	// rather than wrapping onto a second physical line (which would desync the
	// renderer's line count and walk the frame down the screen).
	rn.enter()
	if got := buf.String(); !strings.Contains(got, hideCur) || !strings.Contains(got, wrapOff) {
		t.Errorf("enter: wrote %q, want it to contain %q and %q", got, hideCur, wrapOff)
	}

	buf.Reset()
	rn.prevLines = 3
	rn.clear()
	if got := buf.String(); !strings.Contains(got, showCur) || !strings.Contains(got, wrapOn) {
		t.Errorf("clear: wrote %q, want it to contain %q and %q", got, showCur, wrapOn)
	}
	if rn.prevLines != 0 {
		t.Errorf("clear: prevLines = %d, want 0", rn.prevLines)
	}
}

func TestReadKeyWindowsAndQuit(t *testing.T) {
	br := bufio.NewReader(strings.NewReader("\x17\x03"))
	if k, _, err := readKey(br); err != nil || k != keyWindows {
		t.Fatalf("readKey(Ctrl-W) = %v, %v; want keyWindows", k, err)
	}
	if k, _, err := readKey(br); err != nil || k != keyQuit {
		t.Fatalf("readKey(Ctrl-C) = %v, %v; want keyQuit", k, err)
	}
}

func TestReadKeyEscapeAborts(t *testing.T) {
	br := bufio.NewReader(strings.NewReader("\x1b"))
	if k, _, err := readKey(br); err != nil || k != keyAbort {
		t.Fatalf("readKey(lone Escape) = %v, %v; want keyAbort", k, err)
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
