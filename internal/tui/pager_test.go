package tui

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jskoll/wyrm/internal/tmux"
)

func TestPagerModeOpenAndExit(t *testing.T) {
	m := New(nopRunner(), nil)
	m.width, m.height, m.ready = 100, 30, true
	m.panes = []tmux.PaneInfo{{ID: "%1", Command: "zsh"}}
	m.cur[panelPanes] = 0
	m.focus = panelPanes

	// Send pager message
	scrollback := "line 1\nline 2\nline 3\nline 4\nline 5"
	m, _ = update(m, pagerMsg{paneID: "%1", title: "test", content: scrollback})

	if m.mode != modePager {
		t.Fatalf("mode = %d, want modePager", m.mode)
	}
	if len(m.pagerLines) != 5 {
		t.Fatalf("pagerLines count = %d, want 5", len(m.pagerLines))
	}

	view := m.View()
	if !strings.Contains(view, "Pager: test") {
		t.Errorf("expected view to contain 'Pager: test', got:\n%s", view)
	}
	if !strings.Contains(view, "line 1") {
		t.Errorf("expected view to contain 'line 1', got:\n%s", view)
	}

	// Exit pager with 'q'
	m, _ = update(m, key("q"))
	if m.mode != modeNormal {
		t.Errorf("mode after 'q' = %d, want modeNormal", m.mode)
	}
}

func TestPagerSearchAndNavigation(t *testing.T) {
	m := New(nopRunner(), nil)
	m.width, m.height, m.ready = 100, 10, true

	lines := []string{
		"alpha",
		"target one",
		"beta",
		"target two",
		"gamma",
	}
	m, _ = update(m, pagerMsg{paneID: "%1", title: "search test", content: strings.Join(lines, "\n")})

	// Enter search mode with '/'
	m, _ = update(m, key("/"))
	if !m.pagerSearching {
		t.Fatalf("expected pagerSearching to be true after '/'")
	}

	// Type 'target'
	for _, r := range "target" {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if m.pagerQuery != "target" {
		t.Errorf("pagerQuery = %q, want 'target'", m.pagerQuery)
	}
	if len(m.pagerMatches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(m.pagerMatches))
	}

	// Commit search with Enter
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.pagerSearching {
		t.Errorf("expected pagerSearching false after enter")
	}

	// Jump to next match with 'n'
	m, _ = update(m, key("n"))
	if m.pagerMatchIdx != 1 {
		t.Errorf("pagerMatchIdx = %d, want 1", m.pagerMatchIdx)
	}

	// Jump back with 'N'
	m, _ = update(m, key("N"))
	if m.pagerMatchIdx != 0 {
		t.Errorf("pagerMatchIdx = %d, want 0", m.pagerMatchIdx)
	}

	// Exit with esc
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != modeNormal {
		t.Errorf("mode = %d, want modeNormal", m.mode)
	}
}

// TestPagerEndReachesTheActualLastRenderedLine is the regression test for the
// pager using two different viewport heights: the box itself renders
// height-5 lines (border, title, and border again), but scrolling and paging
// used to assume height-4 — one row taller — so "G"/end stopped one line
// short of the real bottom of the buffer.
func TestPagerEndReachesTheActualLastRenderedLine(t *testing.T) {
	m := New(nopRunner(), nil)
	m.width, m.height, m.ready = 100, 20, true

	lines := make([]string, 50)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	m, _ = update(m, pagerMsg{paneID: "%1", title: "t", content: strings.Join(lines, "\n")})

	m, _ = update(m, key("G"))

	bodyH := m.pagerBodyHeight()
	wantScroll := len(lines) - bodyH
	if m.pagerScroll != wantScroll {
		t.Fatalf("pagerScroll after G = %d, want %d (bodyH=%d)", m.pagerScroll, wantScroll, bodyH)
	}

	view := m.View()
	lastLine := lines[len(lines)-1]
	if !strings.Contains(view, lastLine) {
		t.Errorf("view does not contain %q after G:\n%s", lastLine, view)
	}
}

// TestHighlightMatchUnicodeCaseFolding is the regression test for a pager
// search crash: highlightMatch used to find a match's byte offset in a
// lowercased copy of the line, then slice the *original* string with it using
// the *query's* original byte length. strings.ToLower can change a
// character's byte length — the Kelvin sign ("K", U+212A) lowercases to
// ASCII "k" — so searching it against a line containing a plain "k" sliced
// past a match that was really only one byte long, and could panic.
func TestHighlightMatchUnicodeCaseFolding(t *testing.T) {
	line := "cold as ice, 0k"
	query := "K" // Kelvin sign, lowercases to ASCII "k"

	got := highlightMatch(line, query)
	if !strings.Contains(got, "k") {
		t.Errorf("highlightMatch(%q, %q) = %q, want it to still contain the matched %q", line, query, got, "k")
	}

	// A multi-byte match must come back intact, not split mid-rune.
	got2 := highlightMatch("naïve café", "é")
	if !strings.Contains(got2, "é") {
		t.Errorf("highlightMatch multi-byte match = %q, want it to contain %q", got2, "é")
	}
}

// TestPagerSearchBackspaceTrimsWholeRune is the regression test for the pager
// search box's backspace handler dropping the last byte of the query instead
// of the last rune, which corrupted a multi-byte character (e.g. "é") into
// invalid UTF-8.
func TestPagerSearchBackspaceTrimsWholeRune(t *testing.T) {
	m := New(nopRunner(), nil)
	m.width, m.height, m.ready = 100, 10, true
	m, _ = update(m, pagerMsg{paneID: "%1", title: "t", content: "café"})

	m, _ = update(m, key("/"))
	for _, r := range "café" {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if m.pagerQuery != "café" {
		t.Fatalf("pagerQuery = %q, want café", m.pagerQuery)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.pagerQuery != "caf" {
		t.Errorf("pagerQuery after backspace = %q, want caf", m.pagerQuery)
	}
	if !utf8.ValidString(m.pagerQuery) {
		t.Errorf("pagerQuery = %q is not valid UTF-8", m.pagerQuery)
	}
}
