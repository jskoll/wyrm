package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jskoll/wyrm/internal/config"
)

// TestStartProjectUsesConfigDirNotCwd is the regression test for the TUI
// starting projects in the wrong directory.
//
// startProjectCmd loads a config and hands it to session.Create, which
// resolved a relative session.root against the *process's* working directory.
// Run `wyrm tui` from ~, pick a shared project whose config says root = ".",
// press enter, and every pane opened in ~ instead of the project — nvim on
// nothing, the dev server in the wrong tree. A relative root now resolves
// against the directory the config was loaded from.
func TestStartProjectUsesConfigDirNotCwd(t *testing.T) {
	projectDir := t.TempDir()
	path := filepath.Join(projectDir, config.DefaultFileName)
	content := "[session]\nname = \"proj\"\nroot = \".\"\n\n[[windows]]\nname = \"w\"\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Stand somewhere else entirely — this is the "wyrm tui from ~" case.
	elsewhere := t.TempDir()
	t.Chdir(elsewhere)

	var calls []string
	r := funcRunner{fn: func(args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		switch args[0] {
		case "list-sessions":
			return "", nil
		case "new-session":
			return "$1|proj|@1|%1", nil
		}
		return "", nil
	}}

	if msg := startProjectCmd(r, nil, Project{Path: path})(); msg == nil {
		t.Fatal("startProjectCmd produced no message")
	} else if started, ok := msg.(projectStartedMsg); !ok {
		t.Fatalf("startProjectCmd produced %T, want projectStartedMsg", msg)
	} else if started.err != nil {
		t.Fatalf("startProjectCmd: %v", started.err)
	}

	var newSession string
	for _, c := range calls {
		if strings.HasPrefix(c, "new-session") {
			newSession = c
		}
	}
	if newSession == "" {
		t.Fatalf("new-session was never issued: %v", calls)
	}
	// Resolve both sides: macOS temp dirs are symlinked (/var -> /private/var).
	wantRoot, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	gotRoot := newSession[strings.LastIndex(newSession, " -c ")+len(" -c "):]
	gotRoot, err = filepath.EvalSymlinks(gotRoot)
	if err != nil {
		t.Fatalf("resolving %q: %v", gotRoot, err)
	}
	if gotRoot != wantRoot {
		t.Errorf("new-session -c %q, want the config's own directory %q", gotRoot, wantRoot)
	}
	if strings.Contains(newSession, elsewhere) {
		t.Errorf("session rooted at the process's cwd instead of the project:\n%s", newSession)
	}
}
