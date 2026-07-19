// Package picker is an interactive, dependency-free chooser for running tmux
// sessions. It delivers the fzf experience — type-to-filter fuzzy matching,
// arrow-key navigation — compiled into the binary, so wyrm keeps its "one
// static binary, nothing at runtime but tmux" promise.
//
// The pure pieces (listing, parsing, fuzzy matching, the list model) are kept
// separate from the raw-terminal loop so they can be unit-tested through the
// tmux.Runner mock, the same way tmux.Attach stays out of Runner because it
// needs the process's stdio.
package picker

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jskoll/wyrm/internal/tmux"
	"golang.org/x/term"
)

// Session is a running tmux session shown in the picker.
type Session struct {
	ID       string    `json:"id" toml:"id"`
	Name     string    `json:"name" toml:"name"`
	Windows  int       `json:"windows" toml:"windows"`
	Attached bool      `json:"attached" toml:"attached"`
	Activity time.Time `json:"activity" toml:"activity"`
}

// listFormat mirrors the pipe-separated fields parseSession expects. tmux
// rewrites control characters (including tabs) in -F output to "_", so a
// printable delimiter is required. The session name — the only field that may
// contain the delimiter — is emitted last so a fixed-count SplitN keeps it
// whole even when it holds a "|". The session ID (e.g. "$3") never contains
// "|" and is used to target this session unambiguously afterward — see
// tmux.FindSessionID for why the name itself isn't a safe tmux target.
const listFormat = "#{session_id}|#{session_windows}|#{?session_attached,1,0}|#{session_activity}|#{session_name}"

// ListSessions returns the running tmux sessions, most-recently-active first.
// When no tmux server is running it returns an empty slice and no error, so
// callers can treat "nothing to pick" as an ordinary outcome.
func ListSessions(r tmux.Runner) ([]Session, error) {
	out, err := r.Run("list-sessions", "-F", listFormat)
	if err != nil {
		// With no server up, tmux fails rather than printing an empty list.
		// The wording varies: "no server running on <socket>" for the default
		// server, "error connecting to <socket> (No such file or directory)"
		// for an -L socket that was never created. Treat both as "nothing to
		// pick" rather than an error.
		msg := strings.ToLower(out)
		if strings.Contains(msg, "no server running") || strings.Contains(msg, "error connecting") {
			return nil, nil
		}
		return nil, fmt.Errorf("listing sessions: %v (%s)", err, out)
	}
	var sessions []Session
	for _, line := range strings.Split(out, "\n") {
		if s, ok := parseSession(strings.TrimRight(line, "\r")); ok {
			sessions = append(sessions, s)
		}
	}
	sortSessions(sessions)
	return sessions, nil
}

func parseSession(line string) (Session, bool) {
	if strings.TrimSpace(line) == "" {
		return Session{}, false
	}
	// SplitN with n=5 so a "|" in the name (the last field) is preserved.
	f := strings.SplitN(line, "|", 5)
	if len(f) < 5 {
		return Session{}, false
	}
	windows, _ := strconv.Atoi(f[1])
	epoch, _ := strconv.ParseInt(f[3], 10, 64)
	return Session{
		ID:       f[0],
		Name:     f[4],
		Windows:  windows,
		Attached: f[2] == "1",
		Activity: time.Unix(epoch, 0),
	}, true
}

// sortSessions orders by most recent activity, then name for a stable tie-break.
func sortSessions(s []Session) {
	sort.SliceStable(s, func(i, j int) bool {
		if !s[i].Activity.Equal(s[j].Activity) {
			return s[i].Activity.After(s[j].Activity)
		}
		return s[i].Name < s[j].Name
	})
}

// KillSession destroys a session by its tmux session ID (e.g. "$3") — see
// tmux.FindSessionID for why a raw session name isn't used as a tmux target.
// Unlike session.Kill it runs no lifecycle hooks: the picker operates on
// arbitrary running sessions whose config we don't have, so this is a plain
// tmux kill.
func KillSession(r tmux.Runner, id string) error {
	if out, err := r.Run("kill-session", "-t", id); err != nil {
		return fmt.Errorf("killing session %q: %v (%s)", id, err, out)
	}
	return nil
}

// fuzzyMatch reports whether query is a subsequence of target (case-insensitive)
// along with a score; higher is better. Contiguous runs and matches at a word
// boundary score higher, so "dev" ranks "dev-api" above "d-e-v". An empty query
// matches everything with score 0, preserving the caller's input order.
func fuzzyMatch(query, target string) (int, bool) {
	if query == "" {
		return 0, true
	}
	q := strings.ToLower(query)
	t := strings.ToLower(target)

	score, streak, qi, prevTi := 0, 0, 0, -2
	for ti := 0; ti < len(t) && qi < len(q); ti++ {
		if t[ti] != q[qi] {
			continue
		}
		score++ // base hit
		if ti == prevTi+1 {
			streak++
			score += 3 + streak // consecutive matches dominate
		} else {
			streak = 0
			if ti == 0 || isBoundary(t[ti-1]) {
				score += 2 // start-of-word bonus
			}
		}
		prevTi = ti
		qi++
	}
	if qi != len(q) {
		return 0, false
	}
	return score, true
}

func isBoundary(b byte) bool {
	switch b {
	case '-', '_', ' ', '.', '/', ':', '@':
		return true
	}
	return false
}

// model holds the picker's filtering state. It is pure and unit-tested; the
// interactive loop drives it in response to key presses.
type model struct {
	all      []Session // full session list, activity-ordered
	query    string
	filtered []Session // subset matching query, best score first
	cursor   int

	// viewingWindows and the fields below hold a drill-down into one
	// session's windows (Ctrl-W), replacing the session list until the user
	// picks a window or backs out with Esc. There's no query here — window
	// counts per session are small enough that a plain list suffices, and
	// it sidesteps needing a second reserved key to distinguish "type to
	// filter windows" from "type to filter sessions".
	viewingWindows bool
	windowSession  Session
	windows        []tmux.WindowInfo
	windowCursor   int
}

func newModel(sessions []Session) *model {
	m := &model{all: sessions}
	m.filter()
	return m
}

func (m *model) filter() {
	type scored struct {
		s     Session
		score int
		order int
	}
	var matches []scored
	for i, s := range m.all {
		if score, ok := fuzzyMatch(m.query, s.Name); ok {
			matches = append(matches, scored{s, score, i})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].order < matches[j].order // keep activity order on ties
	})
	m.filtered = m.filtered[:0]
	for _, mt := range matches {
		m.filtered = append(m.filtered, mt.s)
	}
	m.clampCursor()
}

func (m *model) clampCursor() {
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *model) moveUp() {
	if m.cursor > 0 {
		m.cursor--
	}
}

func (m *model) moveDown() {
	if m.cursor < len(m.filtered)-1 {
		m.cursor++
	}
}

func (m *model) appendRune(ch rune) {
	m.query += string(ch)
	m.filter()
}

func (m *model) backspace() {
	if m.query == "" {
		return
	}
	r := []rune(m.query)
	m.query = string(r[:len(r)-1])
	m.filter()
}

func (m *model) selected() (Session, bool) {
	if m.cursor < 0 || m.cursor >= len(m.filtered) {
		return Session{}, false
	}
	return m.filtered[m.cursor], true
}

// enterWindows switches the model into the window-drill-down view for
// session s. windows must already be fetched (Run does the tmux call; the
// model stays pure and side-effect-free).
func (m *model) enterWindows(s Session, windows []tmux.WindowInfo) {
	m.viewingWindows = true
	m.windowSession = s
	m.windows = windows
	m.windowCursor = 0
}

// exitWindows returns to the session list, preserving its query/cursor.
func (m *model) exitWindows() {
	m.viewingWindows = false
	m.windowSession = Session{}
	m.windows = nil
	m.windowCursor = 0
}

func (m *model) moveWindowUp() {
	if m.windowCursor > 0 {
		m.windowCursor--
	}
}

func (m *model) moveWindowDown() {
	if m.windowCursor < len(m.windows)-1 {
		m.windowCursor++
	}
}

func (m *model) selectedWindow() (tmux.WindowInfo, bool) {
	if m.windowCursor < 0 || m.windowCursor >= len(m.windows) {
		return tmux.WindowInfo{}, false
	}
	return m.windows[m.windowCursor], true
}

// Run shows the interactive picker and returns the chosen session's tmux ID
// (e.g. "$3" — see tmux.FindSessionID for why the ID rather than the name),
// or "" if the user aborted or there are no sessions to pick.
func Run(r tmux.Runner) (string, error) {
	sessions, err := ListSessions(r)
	if err != nil {
		return "", err
	}
	if len(sessions) == 0 {
		fmt.Fprintln(os.Stderr, "wyrm: no running tmux sessions")
		return "", nil
	}

	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", fmt.Errorf("opening /dev/tty: %w", err)
	}
	defer func() { _ = tty.Close() }()

	fd := int(tty.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return "", fmt.Errorf("entering raw mode: %w", err)
	}
	defer func() { _ = term.Restore(fd, oldState) }()

	rn := &renderer{w: tty}
	rn.hideCursor()
	defer rn.clear()

	m := newModel(sessions)
	br := bufio.NewReader(tty)

	for {
		_, height, sizeErr := term.GetSize(fd)
		if sizeErr != nil || height < 3 {
			height = len(m.filtered) + 2
		}
		rn.draw(m, height-2)

		key, ch, err := readKey(br)
		if err != nil {
			return "", err
		}
		switch key {
		case keyEnter:
			if m.viewingWindows {
				w, ok := m.selectedWindow()
				if !ok {
					break
				}
				if err := selectWindow(r, w.ID); err != nil {
					return "", err
				}
				return m.windowSession.ID, nil
			}
			if s, ok := m.selected(); ok {
				return s.ID, nil
			}
		case keyAbort:
			if m.viewingWindows {
				m.exitWindows()
				break
			}
			return "", nil
		case keyQuit:
			return "", nil
		case keyUp:
			if m.viewingWindows {
				m.moveWindowUp()
			} else {
				m.moveUp()
			}
		case keyDown:
			if m.viewingWindows {
				m.moveWindowDown()
			} else {
				m.moveDown()
			}
		case keyWindows:
			if m.viewingWindows {
				break
			}
			s, ok := m.selected()
			if !ok {
				break
			}
			windows, listErr := tmux.ListWindows(r, s.ID)
			if listErr != nil || len(windows) == 0 {
				break // session vanished or came up empty; stay put
			}
			m.enterWindows(s, windows)
		case keyKill:
			if m.viewingWindows {
				break
			}
			s, ok := m.selected()
			if !ok {
				break
			}
			_ = KillSession(r, s.ID) // ignore: it may already be gone
			remaining, listErr := ListSessions(r)
			if listErr != nil || len(remaining) == 0 {
				return "", listErr
			}
			q := m.query
			m = newModel(remaining)
			m.query = q
			m.filter()
		case keyBackspace:
			if !m.viewingWindows {
				m.backspace()
			}
		case keyRune:
			if !m.viewingWindows {
				m.appendRune(ch)
			}
		}
	}
}

// selectWindow activates windowID (e.g. "@3") within its session, without
// requiring a client to be attached — attaching or switching afterward
// (attachOrSwitch in main.go) then lands on this window. Mirrors
// KillSession: a raw tmux call with no wyrm-specific bookkeeping.
func selectWindow(r tmux.Runner, windowID string) error {
	if out, err := r.Run("select-window", "-t", windowID); err != nil {
		return fmt.Errorf("selecting window %q: %v (%s)", windowID, err, out)
	}
	return nil
}

// key classifies a decoded key press.
type key int

const (
	keyNone key = iota
	keyRune
	keyEnter
	keyAbort
	keyQuit
	keyUp
	keyDown
	keyBackspace
	keyKill
	keyWindows
)

// readKey decodes one key press, resolving the common escape sequences for
// arrow and delete keys. A lone Escape (no bytes queued behind it) backs out
// one level (or quits, at the top level); Ctrl-C always quits outright.
func readKey(br *bufio.Reader) (key, rune, error) {
	b, err := br.ReadByte()
	if err != nil {
		return keyNone, 0, err
	}
	switch b {
	case '\r', '\n':
		return keyEnter, 0, nil
	case 3: // Ctrl-C
		return keyQuit, 0, nil
	case 16: // Ctrl-P
		return keyUp, 0, nil
	case 14: // Ctrl-N
		return keyDown, 0, nil
	case 23: // Ctrl-W
		return keyWindows, 0, nil
	case 24: // Ctrl-X
		return keyKill, 0, nil
	case 8, 127: // Backspace / Ctrl-H
		return keyBackspace, 0, nil
	case 27: // Escape or an escape sequence
		if br.Buffered() == 0 {
			return keyAbort, 0, nil
		}
		b2, _ := br.ReadByte()
		if b2 != '[' && b2 != 'O' {
			return keyNone, 0, nil
		}
		b3, _ := br.ReadByte()
		switch b3 {
		case 'A':
			return keyUp, 0, nil
		case 'B':
			return keyDown, 0, nil
		case '3': // Delete: ESC [ 3 ~
			_, _ = br.ReadByte() // consume the trailing '~'
			return keyKill, 0, nil
		}
		return keyNone, 0, nil
	}
	if b >= 0x80 { // start of a multi-byte UTF-8 rune
		_ = br.UnreadByte()
		ch, _, err := br.ReadRune()
		if err != nil {
			return keyNone, 0, err
		}
		return keyRune, ch, nil
	}
	if b >= 0x20 && b < 0x7f {
		return keyRune, rune(b), nil
	}
	return keyNone, 0, nil
}

// ANSI control sequences used by the renderer. bold/dim/reverse are text
// attributes, not color, and stay on regardless of NO_COLOR — they read fine
// on a monochrome terminal. green and cyan are actual color accents; see
// colorize for how NO_COLOR suppresses them.
const (
	esc       = "\x1b"
	clearDown = esc + "[J"
	clearLine = esc + "[2K"
	reverse   = esc + "[7m"
	dim       = esc + "[2m"
	bold      = esc + "[1m"
	reset     = esc + "[0m"
	fgReset   = esc + "[39m"
	hideCur   = esc + "[?25l"
	showCur   = esc + "[?25h"

	green = esc + "[32m"
	cyan  = esc + "[36m"
)

// colorize wraps s in an ANSI color code, resetting only the foreground
// color afterward (fgReset, not the full SGR reset) so it composes with an
// outer attribute like the selected row's reverse-video instead of
// cancelling it partway through the line. It returns s unchanged when
// NO_COLOR is set — https://no-color.org: present, regardless of value,
// means no color.
func colorize(code, s string) string {
	if _, noColor := os.LookupEnv("NO_COLOR"); noColor {
		return s
	}
	return code + s + fgReset
}

// renderer redraws the picker in place, tracking how many lines it last drew so
// the next frame can move the cursor back up and overwrite them instead of
// scrolling the terminal.
type renderer struct {
	w         io.Writer
	prevLines int
}

func (rn *renderer) hideCursor() { _, _ = io.WriteString(rn.w, hideCur) }

func (rn *renderer) draw(m *model, maxRows int) {
	if maxRows < 1 {
		maxRows = 1
	}
	var b strings.Builder
	if rn.prevLines > 0 {
		fmt.Fprintf(&b, "%s[%dA", esc, rn.prevLines) // move cursor to top of frame
	}
	b.WriteString(clearDown)

	lines := 0
	writeLine := func(s string) {
		b.WriteString(clearLine)
		b.WriteString(s)
		b.WriteString("\r\n")
		lines++
	}

	if m.viewingWindows {
		drawWindows(m, maxRows, writeLine)
	} else {
		drawSessions(m, maxRows, writeLine)
	}

	rn.prevLines = lines
	_, _ = io.WriteString(rn.w, b.String())
}

func drawSessions(m *model, maxRows int, writeLine func(string)) {
	writeLine(fmt.Sprintf("%s> %s%s", bold, reset, m.query))

	start, end := viewport(m.cursor, len(m.filtered), maxRows)
	for i := start; i < end; i++ {
		row := formatRow(m.filtered[i])
		if i == m.cursor {
			writeLine(reverse + "> " + row + reset)
		} else {
			writeLine("  " + row)
		}
	}
	if len(m.filtered) == 0 {
		writeLine(dim + "  (no matching sessions)" + reset)
	}

	writeLine(fmt.Sprintf("%s  %d/%d · up/down move · enter attach · ctrl-x kill · ctrl-w windows · esc quit%s",
		dim, len(m.filtered), len(m.all), reset))
}

func drawWindows(m *model, maxRows int, writeLine func(string)) {
	writeLine(fmt.Sprintf("%swindows of %s%s", bold, m.windowSession.Name, reset))

	start, end := viewport(m.windowCursor, len(m.windows), maxRows)
	for i := start; i < end; i++ {
		row := formatWindowRow(m.windows[i])
		if i == m.windowCursor {
			writeLine(reverse + "> " + row + reset)
		} else {
			writeLine("  " + row)
		}
	}

	writeLine(fmt.Sprintf("%s  %d windows · up/down move · enter switch · esc back%s",
		dim, len(m.windows), reset))
}

// clear erases the picker UI and restores the cursor, leaving the terminal
// clean before wyrm attaches to (or switches to) the chosen session.
func (rn *renderer) clear() {
	var b strings.Builder
	if rn.prevLines > 0 {
		fmt.Fprintf(&b, "%s[%dA", esc, rn.prevLines)
	}
	b.WriteString(clearDown)
	b.WriteString(showCur)
	_, _ = io.WriteString(rn.w, b.String())
	rn.prevLines = 0
}

// viewport returns the [start,end) slice of rows to show so the cursor stays
// visible within maxRows.
func viewport(cursor, n, maxRows int) (int, int) {
	if n <= maxRows {
		return 0, n
	}
	start := 0
	if cursor >= maxRows {
		start = cursor - maxRows + 1
	}
	end := start + maxRows
	if end > n {
		end = n
	}
	return start, end
}

func formatRow(s Session) string {
	unit := "windows"
	if s.Windows == 1 {
		unit = "window"
	}
	count := colorize(cyan, fmt.Sprintf("%d %s", s.Windows, unit))
	att := ""
	if s.Attached {
		att = "  " + colorize(green, "(attached)")
	}
	return fmt.Sprintf("%-24s %s%s", s.Name, count, att)
}

func formatWindowRow(w tmux.WindowInfo) string {
	if w.Active {
		return w.Name + "  " + colorize(green, "(active)")
	}
	return w.Name
}
