package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/jskoll/wyrm/internal/tmux"
)

// withColor forces a color profile for the duration of a test.
//
// Every other test in this package renders with lipgloss's default profile,
// which in a non-TTY test process is Ascii — Render then returns bare strings
// with no escape sequences at all. That is exactly why the selection-highlight
// bug survived: the styling the tests asserted on wasn't being emitted.
func withColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

const (
	reverseOn = "\x1b[7m"
	sgrReset  = "\x1b[0m"
)

// TestSelectedRowHighlightSpansWholeRow is the regression test for the
// highlight dying partway along the line.
//
// Rendering a row as one pre-styled string and wrapping it in a reverse-video
// style does not work: lipgloss ends every styled run with a full SGR reset
// (termenv's Style.Styled appends ESC[0m) and does not re-apply the outer
// style afterward. Since a Windows row starts with a colored "N:" index, the
// reverse video switched off immediately after it and the window name — the
// part you actually read — was never highlighted, on every row.
func TestSelectedRowHighlightSpansWholeRow(t *testing.T) {
	withColor(t)

	m := New(nopRunner(), nil)
	m.width, m.height = 100, 40
	m.ready = true
	m.focus = panelWindows
	m.windows = []tmux.WindowInfo{
		{Index: 0, ID: "@1", Name: "editor"},
		{Index: 1, ID: "@2", Name: "server"},
	}
	m.windowCur = 0

	rendered := m.renderWindows(30, 8)
	line := selectedLine(t, rendered, "editor")

	// After the last reset in the line there must be no un-highlighted text:
	// every visible run has to carry the reverse attribute.
	if !strings.Contains(line, reverseOn) {
		t.Fatalf("selected row carries no reverse-video attribute:\n%q", line)
	}
	tail := line[strings.LastIndex(line, sgrReset)+len(sgrReset):]
	if strings.TrimSpace(stripANSI(tail)) != "" {
		t.Errorf("text after the final reset is not highlighted: %q\nfull line: %q", tail, line)
	}
	// The name must sit inside a reverse-video run, not after a reset.
	nameAt := strings.Index(line, "editor")
	if nameAt < 0 {
		t.Fatalf("window name missing from row:\n%q", line)
	}
	if !inReverse(line[:nameAt]) {
		t.Errorf("window name is not inside a reverse-video run:\n%q", line)
	}
}

// TestUnselectedRowHasNoHighlight keeps the above honest — a test that passed
// because everything was reversed would be no test at all.
func TestUnselectedRowHasNoHighlight(t *testing.T) {
	withColor(t)

	m := New(nopRunner(), nil)
	m.width, m.height = 100, 40
	m.ready = true
	m.focus = panelWindows
	m.windows = []tmux.WindowInfo{
		{Index: 0, ID: "@1", Name: "editor"},
		{Index: 1, ID: "@2", Name: "server"},
	}
	m.windowCur = 0

	line := selectedLine(t, m.renderWindows(30, 8), "server")
	if strings.Contains(line, reverseOn) {
		t.Errorf("unselected row should not be reversed:\n%q", line)
	}
}

// selectedLine returns the rendered line containing want.
func selectedLine(t *testing.T, rendered, want string) string {
	t.Helper()
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, want) {
			return line
		}
	}
	t.Fatalf("no rendered line contains %q:\n%s", want, rendered)
	return ""
}

// inReverse reports whether the reverse attribute is still in effect at the
// end of prefix: a reverse-on with no full reset after it.
func inReverse(prefix string) bool {
	lastOn := strings.LastIndex(prefix, reverseOn)
	if lastOn < 0 {
		return false
	}
	return strings.LastIndex(prefix, sgrReset) < lastOn
}

// stripANSI removes CSI sequences so the visible text can be inspected.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && (s[i] < 0x40 || s[i] > 0x7e) {
				i++
			}
			i++
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
