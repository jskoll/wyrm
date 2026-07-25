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
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/ansi"
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
		// No server up isn't an error here — it just means nothing to pick.
		if tmux.NoServerRunning(out) {
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
	if !tmux.ValidID(tmux.SessionSigil, f[0]) {
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

// FuzzyMatch exposes the picker's matcher to the TUI, so its panel filter
// ranks the same way the picker's does rather than reimplementing subsequence
// matching a second time.
func FuzzyMatch(query, target string) (int, bool) { return fuzzyMatch(query, target) }

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

	// confirmKill is set while a Ctrl-X is awaiting a y/n answer.
	confirmKill bool
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
// or "" if the user aborted or there are no sessions to pick. stderr is
// where the "nothing to pick" notice goes — passed in rather than hardcoded
// so callers can capture or redirect it, the same way main.run threads
// stdout/stderr throughout the CLI.
//
// Run itself only sets up the real terminal (opening /dev/tty, entering raw
// mode); the read-key/update/redraw cycle lives in runLoop, kept separate so
// it can be driven by an in-memory reader/writer in tests instead of a real
// tty — see runLoop's doc.
func Run(r tmux.Runner, stderr io.Writer) (string, error) {
	sessions, err := ListSessions(r)
	if err != nil {
		return "", err
	}
	if len(sessions) == 0 {
		_, _ = fmt.Fprintln(stderr, "wyrm: no running tmux sessions")
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

	// Restore the terminal on the signals that would otherwise kill the process
	// outright. term.Restore is deferred, but a deferred call doesn't run for
	// SIGTERM or SIGHUP — and the picker has by then disabled echo and line
	// editing and hidden the cursor, so the user is left with an unusable shell
	// they have to type "stty sane" into blind. Reachable from `pkill wyrm`,
	// closing the terminal emulator, or killing the tmux pane it runs in.
	fatal := make(chan os.Signal, 1)
	signal.Notify(fatal, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(fatal)

	rn := &renderer{w: tty}
	rn.enter()
	defer rn.clear()

	go func() {
		sig, ok := <-fatal
		if !ok {
			return
		}
		rn.clear()
		_ = term.Restore(fd, oldState)
		// Re-raise with the handler removed, so the process still dies of the
		// signal rather than exiting 0.
		signal.Reset(sig)
		if s, ok := sig.(syscall.Signal); ok {
			_ = syscall.Kill(os.Getpid(), s)
		}
		os.Exit(1)
	}()

	// SIGWINCH redraws at the new size. Without it a resize did nothing until
	// the next keypress, and the renderer's idea of how many physical lines it
	// had written was already wrong by then — so its cursor-up reposition
	// undershot and smeared stale frames down the screen.
	resize := make(chan os.Signal, 1)
	signal.Notify(resize, syscall.SIGWINCH)
	defer signal.Stop(resize)

	height := func() int {
		_, h, err := term.GetSize(fd)
		if err != nil {
			return 0
		}
		return h
	}
	return runLoop(r, sessions, bufio.NewReader(tty), rn, height, resize)
}

// runLoop drives the picker's read-key/update/redraw cycle until the user
// picks a session/window or backs out, returning the same result Run does.
// It knows nothing about /dev/tty or raw mode — br and rn can be backed by
// anything, and height reports the terminal's current row count (or any
// value below 3, e.g. 0, when it can't be determined), letting the pure
// filtering/model logic and the terminal plumbing in Run be tested
// independently.
func runLoop(r tmux.Runner, sessions []Session, br *bufio.Reader, rn *renderer, height func() int, resize <-chan os.Signal) (string, error) {
	m := newModel(sessions)
	keys := readKeys(br)

	for {
		h := height()
		if h < 3 {
			// Unknown terminal size (e.g. tests): show every row, no clamp.
			h = len(m.filtered) + 3
		}
		// The frame is maxRows session rows plus two chrome lines (the query
		// line and the footer). Reserve one further row so the whole frame is
		// at most h-1 lines: drawing exactly h lines, each ending in "\r\n",
		// scrolls the terminal on the final newline every keypress, which reads
		// as jitter once the list overflows the viewport.
		rn.draw(m, h-3)

		// A nil resize channel (tests) blocks forever in select, leaving this
		// a plain blocking read.
		var key keyCode
		var ch rune
		select {
		case ev, ok := <-keys:
			if !ok {
				return "", nil
			}
			if ev.err != nil {
				return "", ev.err
			}
			key, ch = ev.key, ev.ch
		case <-resize:
			rn.invalidate()
			continue
		}

		// A pending kill confirmation swallows everything else: y goes through
		// with it, anything else cancels.
		if m.confirmKill {
			m.confirmKill = false
			if key == keyRune && (ch == 'y' || ch == 'Y') {
				if s, ok := m.selected(); ok {
					_ = KillSession(r, s.ID) // it may already be gone
					remaining, listErr := ListSessions(r)
					if listErr != nil || len(remaining) == 0 {
						return "", listErr
					}
					q := m.query
					m = newModel(remaining)
					m.query = q
					m.filter()
				}
			}
			continue
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
			if _, ok := m.selected(); !ok {
				break
			}
			// Ask first. Destroying a session takes every running process in
			// it with it, and the TUI already confirms the same operation.
			m.confirmKill = true
		case keyClearQuery:
			if !m.viewingWindows {
				m.query = ""
				m.filter()
			}
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

// keyCode classifies a decoded key press.
type keyCode int

const (
	keyNone keyCode = iota
	keyRune
	keyEnter
	keyAbort
	keyQuit
	keyUp
	keyDown
	keyBackspace
	keyKill
	keyWindows
	keyClearQuery
)

// keyEvent is one decoded key press, delivered over a channel so the loop can
// wait on a terminal resize at the same time as on input.
type keyEvent struct {
	key keyCode
	ch  rune
	err error
}

// readKeys decodes key presses on a goroutine so runLoop can select between
// input and SIGWINCH. The goroutine ends when the reader errors; on any other
// exit it is left blocked on the buffered send, which is fine for a
// short-lived CLI that is about to attach or exit.
func readKeys(br *bufio.Reader) <-chan keyEvent {
	out := make(chan keyEvent, 1)
	go func() {
		for {
			k, ch, err := readKey(br)
			out <- keyEvent{k, ch, err}
			if err != nil {
				close(out)
				return
			}
		}
	}()
	return out
}

// readKey decodes one key press, resolving the common escape sequences for
// arrow, navigation, and delete keys. A lone Escape (no bytes queued behind
// it) backs out one level (or quits, at the top level); Ctrl-C always quits
// outright.
func readKey(br *bufio.Reader) (keyCode, rune, error) {
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
	case 21: // Ctrl-U: clear the query, as in readline
		return keyClearQuery, 0, nil
	case 23: // Ctrl-W
		return keyWindows, 0, nil
	case 24: // Ctrl-X
		return keyKill, 0, nil
	case 8, 127: // Backspace / Ctrl-H
		return keyBackspace, 0, nil
	case 9: // Tab: drill into the selected session's windows
		return keyWindows, 0, nil
	case 27: // Escape or an escape sequence
		if br.Buffered() == 0 {
			return keyAbort, 0, nil
		}
		b2, _ := br.ReadByte()
		if b2 != '[' && b2 != 'O' {
			return keyNone, 0, nil
		}
		return readCSI(br)
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

// readCSI decodes the body of an "ESC [" / "ESC O" sequence, having already
// consumed the introducer.
//
// It consumes the *whole* sequence even when the result is keyNone. The
// previous version read a single byte and gave up, leaving the rest in the
// buffer to be re-read as literal text — so pressing Home (ESC [ 1 ~) or PgUp
// (ESC [ 5 ~) silently typed a "~" into the fuzzy filter, and a shifted arrow
// typed ";2A".
func readCSI(br *bufio.Reader) (keyCode, rune, error) {
	// Parameter and intermediate bytes, then one final byte in @-~.
	var params []byte
	for {
		b, err := br.ReadByte()
		if err != nil {
			return keyNone, 0, err
		}
		if b >= 0x40 && b <= 0x7e {
			return csiKey(params, b), 0, nil
		}
		params = append(params, b)
	}
}

// csiKey maps a decoded CSI sequence to a picker key. Unrecognized sequences
// (mouse reports, function keys) are ignored rather than typed.
func csiKey(params []byte, final byte) keyCode {
	switch final {
	case 'A':
		return keyUp
	case 'B':
		return keyDown
	}
	// Everything else — Home/End ('H'/'F'), Delete and the PgUp/PgDn family
	// ('~' with a numeric parameter), function keys, mouse reports — is
	// ignored. Delete used to map to "kill session", unconfirmed and
	// undocumented, sitting one key away from Home and End; those in turn
	// leaked their trailing "~" into the fuzzy filter.
	_ = params
	return keyNone
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

	// wrapOff/wrapOn toggle autowrap (DECAWM, mode 7). The picker keeps it off
	// while running so an over-long row (a wide session name, or the footer in
	// a narrow tmux popup) is clipped at the right margin instead of wrapping
	// onto a second physical row. Wrapping would desync the renderer's logical
	// line count from the physical rows on screen, so the per-frame cursor-up
	// reposition undershoots and the frame walks down the screen, leaving a
	// trail of stale header lines. clear restores autowrap on the way out.
	wrapOff = esc + "[?7l"
	wrapOn  = esc + "[?7h"

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

// enter puts the terminal into the mode the picker draws in: cursor hidden and
// autowrap off (see wrapOff). clear reverses both when the picker exits.
func (rn *renderer) enter() { _, _ = io.WriteString(rn.w, hideCur+wrapOff) }

// invalidate forgets how many lines the last frame occupied, so the next draw
// starts where the cursor is instead of moving up by a count that a terminal
// resize has just made wrong.
func (rn *renderer) invalidate() { rn.prevLines = 0 }

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

	if m.confirmKill {
		name := ""
		if s, ok := m.selected(); ok {
			name = s.Name
		}
		writeLine(bold + "  kill session '" + name + "'? (y/n)" + reset)
		return
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

// clear erases the picker UI and restores the cursor and autowrap, leaving the
// terminal clean before wyrm attaches to (or switches to) the chosen session.
func (rn *renderer) clear() {
	var b strings.Builder
	if rn.prevLines > 0 {
		fmt.Fprintf(&b, "%s[%dA", esc, rn.prevLines)
	}
	b.WriteString(clearDown)
	b.WriteString(showCur)
	b.WriteString(wrapOn)
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

// FormatRow renders a session as "name  N window(s)[  (attached)]" — the
// shape shared by the interactive picker's colorized rows and -list's plain
// table (see main.formatSessionRow). colored selects ANSI color for the
// window count and attached marker; colorize still suppresses it on top of
// this when NO_COLOR is set.
func FormatRow(s Session, colored bool) string {
	unit := "windows"
	if s.Windows == 1 {
		unit = "window"
	}
	count := fmt.Sprintf("%d %s", s.Windows, unit)
	att := ""
	if colored {
		count = colorize(cyan, count)
		if s.Attached {
			att = "  " + colorize(green, "(attached)")
		}
	} else if s.Attached {
		att = "  (attached)"
	}
	return padName(s.Name, nameColumn) + " " + count + att
}

// nameColumn is the width the session-name column is padded to.
const nameColumn = 24

// padName fits a session name into a fixed display-width column, truncating
// with an ellipsis when it doesn't fit.
//
// fmt's "%-24s" pads by *rune* count, not display width, so a CJK or emoji
// name — twice as wide on screen as it is long in runes — pushed the window
// count out of alignment for every row. It also never truncated, so a long
// name ran over the count and the "(attached)" marker, which autowrap-off then
// clipped away with no indication anything was missing.
func padName(name string, w int) string {
	if width := ansi.StringWidth(name); width <= w {
		return name + strings.Repeat(" ", w-width)
	}
	truncated := ansi.Truncate(name, w, "…")
	if gap := w - ansi.StringWidth(truncated); gap > 0 {
		truncated += strings.Repeat(" ", gap)
	}
	return truncated
}

func formatRow(s Session) string { return FormatRow(s, true) }

func formatWindowRow(w tmux.WindowInfo) string {
	if w.Active {
		return w.Name + "  " + colorize(green, "(active)")
	}
	return w.Name
}
