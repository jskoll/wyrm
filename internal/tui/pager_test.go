package tui

import (
	"strings"
	"testing"

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
