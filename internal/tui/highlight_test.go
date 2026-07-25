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
//
// TrueColor specifically, so the theme's hex colors come through as exact,
// assertable sequences instead of being rounded to a palette index.
func withColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

const sgrReset = "\x1b[0m"

// fgSGR/bgSGR return the SGR parameters lipgloss emits for a theme color, so
// the assertions below track the theme instead of restating it — swapping the
// palette shouldn't mean rewriting the tests that check the selection still
// spans the row.
//
// The values come from rendering a probe rather than from converting the hex
// here: lipgloss rounds through go-colorful's floats, which lands a channel
// one below the literal hex often enough to matter (#654321 renders as
// 101;67;32). Asking lipgloss what it emits can't drift from what it emits.
func fgSGR(t *testing.T, hex string) string {
	return probeSGR(t, lipgloss.NewStyle().Foreground(lipgloss.Color(hex)), "38;2;")
}

func bgSGR(t *testing.T, hex string) string {
	return probeSGR(t, lipgloss.NewStyle().Background(lipgloss.Color(hex)), "48;2;")
}

func probeSGR(t *testing.T, style lipgloss.Style, prefix string) string {
	t.Helper()
	out := style.Render("x")
	start := strings.Index(out, prefix)
	if start < 0 {
		t.Fatalf("no %q sequence in probe render %q — is the color profile TrueColor?", prefix, out)
	}
	end := strings.Index(out[start:], "m")
	if end < 0 {
		t.Fatalf("unterminated SGR sequence in probe render %q", out)
	}
	return out[start : start+end]
}

// TestSelectedRowHighlightSpansWholeRow is the regression test for the
// highlight dying partway along the line.
//
// Rendering a row as one pre-styled string and wrapping it in a selection
// style does not work: lipgloss ends every styled run with a full SGR reset
// (termenv's Style.Styled appends ESC[0m) and does not re-apply the outer
// style afterward. Since a Windows row starts with a colored "N:" index, the
// highlight switched off immediately after it and the window name — the part
// you actually read — was never highlighted, on every row.
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
	// every visible run has to carry the selection background.
	selBg := bgSGR(t, DefaultTheme().Selected)
	if !strings.Contains(line, selBg) {
		t.Fatalf("selected row carries no selection background:\n%q", line)
	}
	tail := line[strings.LastIndex(line, sgrReset)+len(sgrReset):]
	if strings.TrimSpace(stripANSI(tail)) != "" {
		t.Errorf("text after the final reset is not highlighted: %q\nfull line: %q", tail, line)
	}
	// The name must sit inside a highlighted run, not after a reset.
	nameAt := strings.Index(line, "editor")
	if nameAt < 0 {
		t.Fatalf("window name missing from row:\n%q", line)
	}
	if !inSelection(line[:nameAt], selBg) {
		t.Errorf("window name is not inside a highlighted run:\n%q", line)
	}
}

// TestSelectedRowKeepsSpanForeground pins the reason the selection is a
// background wash rather than reverse video: a span that sets its own color
// keeps it while selected. Inherit fills in only what a span leaves unset, so
// the window index keeps its own color on the selection band — reverse video
// would have swapped it into the background.
func TestSelectedRowKeepsSpanForeground(t *testing.T) {
	withColor(t)

	m := New(nopRunner(), nil)
	m.width, m.height = 100, 40
	m.ready = true
	m.focus = panelWindows
	m.windows = []tmux.WindowInfo{{Index: 0, ID: "@1", Name: "editor"}}
	m.windowCur = 0

	line := selectedLine(t, m.renderWindows(30, 8), "editor")
	idxAt := strings.Index(line, "0:")
	if idxAt < 0 {
		t.Fatalf("window index missing from row:\n%q", line)
	}
	run := line[:idxAt]
	if !strings.Contains(run, fgSGR(t, DefaultTheme().Index)) {
		t.Errorf("selected row dropped the window index's own color:\n%q", line)
	}
	if !inSelection(run, bgSGR(t, DefaultTheme().Selected)) {
		t.Errorf("window index is not inside a highlighted run:\n%q", line)
	}
}

// TestUnselectedRowHasNoHighlight keeps the above honest — a test that passed
// because every row was highlighted would be no test at all.
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
	if strings.Contains(line, bgSGR(t, DefaultTheme().Selected)) {
		t.Errorf("unselected row should not be highlighted:\n%q", line)
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

// inSelection reports whether the selection background selBg is still in
// effect at the end of prefix: a selection run with no full reset after it.
func inSelection(prefix, selBg string) bool {
	lastOn := strings.LastIndex(prefix, selBg)
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
