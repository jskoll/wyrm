package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// projectModel returns a model focused on the Projects panel with one project.
func projectModel(r *funcRunner, path string) Model {
	m := New(r, nil)
	m.focus = panelProjects
	m.projects = []Project{{Name: "webapp", Path: path}}
	m.projectCur = 0
	m.previewSrc = previewConfig
	return m
}

func TestProjectsMsgClampsCursor(t *testing.T) {
	m := New(nopRunner(), nil)
	m.projectCur = 5
	m, _ = update(m, projectsMsg{projects: []Project{{Name: "a"}, {Name: "b"}}})
	if len(m.projects) != 2 {
		t.Fatalf("projects = %d, want 2", len(m.projects))
	}
	if m.projectCur != 1 {
		t.Errorf("projectCur = %d, want clamped to 1", m.projectCur)
	}
}

func TestFocusProjectsLoadsConfigPreview(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".wyrm.toml")
	if err := os.WriteFile(path, []byte("[session]\nname = \"webapp\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := New(nopRunner(), nil)
	m.projects = []Project{{Name: "webapp", Path: path}}
	m.focus = panelSessions // start off Projects

	// Switching focus to Projects (key "1") should request a config preview.
	m, cmd := update(m, key("1"))
	if m.previewSrc != previewConfig {
		t.Fatalf("previewSrc = %d, want previewConfig", m.previewSrc)
	}
	msg := run(cmd)
	cp, ok := msg.(configPreviewMsg)
	if !ok {
		t.Fatalf("focus->Projects produced %T, want configPreviewMsg", msg)
	}
	m, _ = update(m, cp)
	if !strings.Contains(m.preview, "webapp") {
		t.Errorf("config preview = %q, want it to contain the file contents", m.preview)
	}
}

func TestTickSkipsConfigPreview(t *testing.T) {
	m := New(nopRunner(), nil)
	m.focus = panelProjects
	m.previewSrc = previewConfig
	m.preview = "config contents"
	// A tick must not clobber a static config preview with a pane capture.
	m, cmd := update(m, tickMsg{})
	if m.preview != "config contents" {
		t.Errorf("tick overwrote the config preview: %q", m.preview)
	}
	// The ticker must still reschedule itself.
	if run(cmd) == nil {
		t.Error("tick should still reschedule the next tick")
	}
}

func TestStartProjectAttaches(t *testing.T) {
	// Point at a real, minimal config; the funcRunner fakes tmux so no server
	// is needed. session.Create issues new-session and returns its ID.
	dir := t.TempDir()
	path := filepath.Join(dir, ".wyrm.toml")
	cfg := "[session]\nname = \"proj\"\nroot = \"" + dir + "\"\n\n[[windows]]\nname = \"main\"\n"
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &funcRunner{fn: func(args ...string) (string, error) {
		switch args[0] {
		case "list-sessions":
			return "", nil // nothing running yet
		case "new-session":
			return "$9|@1|%1", nil
		}
		return "", nil
	}}
	m := projectModel(r, path)

	m, cmd := update(m, key("enter"))
	msg := run(cmd)
	ps, ok := msg.(projectStartedMsg)
	if !ok {
		t.Fatalf("enter on project produced %T, want projectStartedMsg", msg)
	}
	m, quit := update(m, ps)
	if m.pendingAttach != "$9" {
		t.Errorf("pendingAttach = %q, want $9", m.pendingAttach)
	}
	if _, ok := run(quit).(tea.QuitMsg); !ok {
		t.Error("starting a project should quit to hand off the attach")
	}
}

func TestKillProjectRequiresRunning(t *testing.T) {
	r := &funcRunner{fn: func(_ ...string) (string, error) { return "", nil }}
	m := projectModel(r, "/tmp/x/.wyrm.toml")
	m.projects[0].Running = false
	// x on a not-running project is a no-op (no confirm modal).
	m, _ = update(m, key("x"))
	if m.mode != modeNormal {
		t.Errorf("x on a stopped project should not open a modal")
	}
	// A running project opens the stop-confirm modal.
	m.projects[0].Running = true
	m, _ = update(m, key("x"))
	if m.mode != modeConfirm {
		t.Fatalf("x on a running project should open a confirm modal")
	}
	if m.pending.op != opKillProject {
		t.Errorf("pending.op = %d, want opKillProject", m.pending.op)
	}
	if !strings.Contains(m.confirmPrompt, "on_project_exit") {
		t.Errorf("confirm should mention hooks, got %q", m.confirmPrompt)
	}
}
