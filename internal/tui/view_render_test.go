package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"

	"github.com/jskoll/wyrm/internal/picker"
	"github.com/jskoll/wyrm/internal/tmux"
)

// newSizedModel returns a ready Model at a comfortable size for rendering.
func newSizedModel() Model {
	m := New(nopRunner(), nil)
	m.width, m.height, m.ready = 100, 30, true
	return m
}

func TestViewRendersProjectsWithRunningMark(t *testing.T) {
	m := newSizedModel()
	m.projects = []Project{
		{Name: "webapp", Path: "webapp.wyrm.toml", Running: true},
		{Name: "dotfiles", Path: "dotfiles.wyrm.toml"},
	}

	out := m.View()
	for _, want := range []string{"Projects", "webapp", "dotfiles", "●"} {
		if !strings.Contains(out, want) {
			t.Errorf("View() missing %q\n---\n%s", want, out)
		}
	}
}

func TestViewContextualHelpPerFocus(t *testing.T) {
	m := newSizedModel()
	m.sessions = []picker.Session{{ID: "$1", Name: "s", Windows: 1}}
	m.windows = []tmux.WindowInfo{{Index: 0, ID: "@1", Name: "w"}}
	m.panes = []tmux.PaneInfo{{ID: "%1", Index: 0, Command: "sh"}}

	cases := []struct {
		focus panel
		want  string
		avoid string
	}{
		{panelProjects, "e: edit", "L: layout"},
		{panelSessions, "r: rename", "L: layout"},
		{panelWindows, "L: layout", "z: zoom"},
		{panelPanes, "z: zoom", "L: layout"},
	}
	for _, c := range cases {
		m.focus = c.focus
		out := m.View()
		if !strings.Contains(out, c.want) {
			t.Errorf("focus %d: help line missing %q\n%s", c.focus, c.want, out)
		}
		if strings.Contains(out, c.avoid) {
			t.Errorf("focus %d: help line should not contain %q", c.focus, c.avoid)
		}
	}
}

func TestViewConfirmModal(t *testing.T) {
	m := newSizedModel()
	m.mode = modeConfirm
	m.confirmPrompt = "Kill session 'webapp'?  (y/n)"

	if out := m.View(); !strings.Contains(out, "Kill session 'webapp'?") {
		t.Errorf("confirm modal not rendered in the footer\n%s", out)
	}
}

func TestViewPromptModal(t *testing.T) {
	m := newSizedModel()
	m.mode = modePrompt
	m.promptTitle = "Rename session:"
	ti := textinput.New()
	ti.SetValue("newname")
	m.textInput = ti

	out := m.View()
	if !strings.Contains(out, "Rename session:") {
		t.Errorf("prompt title not rendered\n%s", out)
	}
	if !strings.Contains(out, "newname") {
		t.Errorf("prompt input value not rendered\n%s", out)
	}
}

func TestViewWindowEmptyNameFallback(t *testing.T) {
	m := newSizedModel()
	m.sessions = []picker.Session{{ID: "$1", Name: "s", Windows: 1}}
	m.windows = []tmux.WindowInfo{{Index: 3, ID: "@3", Name: ""}}
	m.focus = panelWindows

	if out := m.View(); !strings.Contains(out, "window 3") {
		t.Errorf("an unnamed window should render as 'window 3'\n%s", out)
	}
}

func TestViewPreviewError(t *testing.T) {
	m := newSizedModel()
	m.previewSrc = previewPane
	m.preview = ""
	m.err = errors.New("capture failed")

	if out := m.View(); !strings.Contains(out, "error: capture failed") {
		t.Errorf("an empty preview with an error should show it\n%s", out)
	}
}

// TestFilteredPanelUsesFilterAccent covers the border/title switching to the
// filter color while a filter is active, and only on the panel that has focus
// — a filter never applies to any other panel, so accenting one would point at
// rows the filter isn't touching.
func TestFilteredPanelUsesFilterAccent(t *testing.T) {
	withColor(t)

	filterFgSGR := fgSGR(t, DefaultTheme().Filter)

	m := newSizedModel()
	m.focus = panelSessions
	m.sessions = []picker.Session{{ID: "$1", Name: "webapp", Windows: 1}}

	if out := m.renderSessions(30, 6); strings.Contains(out, filterFgSGR) {
		t.Errorf("unfiltered panel should not use the filter accent:\n%q", out)
	}

	m.filtering = true
	m.filter = "web"
	if out := m.renderSessions(30, 6); !strings.Contains(out, filterFgSGR) {
		t.Errorf("filtered panel should use the filter accent:\n%q", out)
	}
	// The filter belongs to the focused panel only.
	if out := m.renderWindows(30, 6); strings.Contains(out, filterFgSGR) {
		t.Errorf("unfocused panel should not use the filter accent:\n%q", out)
	}
}

func TestViewConfigPreview(t *testing.T) {
	m := newSizedModel()
	m.focus = panelProjects
	m.projects = []Project{{Name: "webapp", Path: "webapp.wyrm.toml"}}
	m.previewSrc = previewConfig
	m.previewTitle = "webapp.wyrm.toml"
	m.preview = "[session]\nname = 'webapp'"

	out := m.View()
	for _, want := range []string{"webapp.wyrm.toml", "[session]", "name = 'webapp'"} {
		if !strings.Contains(out, want) {
			t.Errorf("config preview missing %q\n%s", want, out)
		}
	}
}
