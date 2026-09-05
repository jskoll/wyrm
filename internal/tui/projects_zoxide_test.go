package tui

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jskoll/wyrm/internal/config"
)

// installFakeZoxide puts a fake "zoxide" script at the front of PATH,
// mirroring internal/zoxide's own test helper — duplicated rather than
// imported so this package's tests don't need to reach into another
// package's test file.
func installFakeZoxide(t *testing.T, queryOutput string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake zoxide script requires a POSIX shell")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"query\" ]; then printf '%s' \"" + queryOutput + "\"; exit 0; fi\n" +
		"if [ \"$1\" = \"add\" ]; then\n" +
		"  if [ -n \"$ZOXIDE_FAKE_ARGS_FILE\" ]; then printf '%s\\n' \"$*\" > \"$ZOXIDE_FAKE_ARGS_FILE\"; fi\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	path := filepath.Join(dir, "zoxide")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func enabledZoxideSettings() *config.Settings {
	on := true
	return &config.Settings{Zoxide: config.Zoxide{Enabled: &on}}
}

// TestListProjectsPropagatesSessionListFailure is the regression test for a
// tmux list-sessions failure being silently discarded: listProjects used to
// treat any error the same as "nothing is running" (an empty running map),
// so every discovered project displayed as stopped instead of surfacing that
// discovery itself was based on incomplete data.
func TestListProjectsPropagatesSessionListFailure(t *testing.T) {
	r := funcRunner{fn: func(args ...string) (string, error) {
		if len(args) > 0 && args[0] == "list-sessions" {
			return "permission denied", errors.New("exit status 1")
		}
		return "", nil
	}}

	if _, err := listProjects(r, nil); err == nil {
		t.Fatal("listProjects = nil error, want the list-sessions failure propagated")
	}
}

// TestListProjectsZoxideDisabledByDefault: without settings.zoxide.enabled,
// zoxide entries never appear, even with zoxide installed and directories to
// offer — this is the "opt-in, gracefully absent" guarantee.
func TestListProjectsZoxideDisabledByDefault(t *testing.T) {
	installFakeZoxide(t, "3.0 /home/user/somewhere\n")
	t.Chdir(t.TempDir())

	projects, err := listProjects(nopRunner(), nil)
	if err != nil {
		t.Fatalf("listProjects: %v", err)
	}
	for _, p := range projects {
		if p.Zoxide {
			t.Errorf("zoxide project %+v appeared with settings=nil (disabled by default)", p)
		}
	}
}

// TestListProjectsZoxideAppendsUnknownDirectories covers the enabled case:
// a zoxide directory with no wyrm config of its own shows up, marked Zoxide.
func TestListProjectsZoxideAppendsUnknownDirectories(t *testing.T) {
	installFakeZoxide(t, "3.0 /home/user/somewhere\n")
	t.Chdir(t.TempDir())

	projects, err := listProjects(nopRunner(), enabledZoxideSettings())
	if err != nil {
		t.Fatalf("listProjects: %v", err)
	}
	found := false
	for _, p := range projects {
		if p.Name == "somewhere" {
			found = true
			if !p.Zoxide || p.Root != "/home/user/somewhere" || p.Path != "" {
				t.Errorf("zoxide project = %+v, want Zoxide=true, Root=/home/user/somewhere, Path empty", p)
			}
		}
	}
	if !found {
		t.Errorf("projects = %+v, want a zoxide entry named 'somewhere'", projects)
	}
}

// TestListProjectsZoxideSkipsNameCollision: a zoxide-known directory whose
// basename matches an already-discovered wyrm project must not produce a
// second, confusing row for the same name.
func TestListProjectsZoxideSkipsNameCollision(t *testing.T) {
	local := t.TempDir()
	t.Chdir(local)
	content := "[session]\nname = \"myproj\"\nroot = \".\"\n\n[[windows]]\nname = \"w\"\n"
	if err := os.WriteFile(filepath.Join(local, config.DefaultFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	installFakeZoxide(t, "3.0 /somewhere/else/myproj\n")

	projects, err := listProjects(nopRunner(), enabledZoxideSettings())
	if err != nil {
		t.Fatalf("listProjects: %v", err)
	}
	count := 0
	for _, p := range projects {
		if p.Name == "myproj" {
			count++
			if p.Zoxide {
				t.Errorf("the zoxide entry won over the real project: %+v", p)
			}
		}
	}
	if count != 1 {
		t.Errorf("got %d rows named myproj, want exactly 1 (no duplicate)", count)
	}
}

// TestStartProjectCmdZoxideUsesDefaultConfig covers building a session for a
// zoxide-only Project: no config.Load, since there's no Path — the built-in
// default config, rooted at the zoxide directory.
func TestStartProjectCmdZoxideUsesDefaultConfig(t *testing.T) {
	dest := t.TempDir()
	var calls []string
	r := funcRunner{fn: func(args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		switch args[0] {
		case "list-sessions":
			return "", nil
		case "new-session":
			return "$1|@1|%1", nil
		case "display-message":
			return filepath.Base(dest), nil
		}
		return "", nil
	}}

	msg := startProjectCmd(r, nil, Project{Name: filepath.Base(dest), Root: dest, Zoxide: true})()
	started, ok := msg.(projectStartedMsg)
	if !ok || started.err != nil {
		t.Fatalf("startProjectCmd = %+v, %v", msg, ok)
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
	gotRoot := newSession[strings.LastIndex(newSession, " -c ")+len(" -c "):]
	if wantRoot, _ := filepath.EvalSymlinks(dest); gotRoot != wantRoot {
		if g, _ := filepath.EvalSymlinks(gotRoot); g != wantRoot {
			t.Errorf("new-session -c %q, want the zoxide directory %q", gotRoot, dest)
		}
	}
}

// TestStartProjectCmdZoxideTrackAddsPath covers track mode: a successful
// build calls `zoxide add` on the project's root when settings.zoxide.track
// is on, and does nothing when it's off (the default).
func TestStartProjectCmdZoxideTrackAddsPath(t *testing.T) {
	dest := t.TempDir()
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("ZOXIDE_FAKE_ARGS_FILE", argsFile)
	installFakeZoxide(t, "")

	r := funcRunner{fn: func(args ...string) (string, error) {
		switch args[0] {
		case "list-sessions":
			return "", nil
		case "new-session":
			return "$1|@1|%1", nil
		case "display-message":
			return filepath.Base(dest), nil
		}
		return "", nil
	}}

	on := true
	settings := &config.Settings{Zoxide: config.Zoxide{Enabled: &on, Track: &on}}
	msg := startProjectCmd(r, settings, Project{Name: filepath.Base(dest), Root: dest, Zoxide: true})()
	if started, ok := msg.(projectStartedMsg); !ok || started.err != nil {
		t.Fatalf("startProjectCmd = %+v, %v", msg, ok)
	}

	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("zoxide add was not called: %v", err)
	}
	if want := "add " + dest; strings.TrimSpace(string(got)) != want {
		t.Errorf("zoxide invoked with %q, want %q", strings.TrimSpace(string(got)), want)
	}
}

func TestStartProjectCmdNoTrackDoesNotAddPath(t *testing.T) {
	dest := t.TempDir()
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("ZOXIDE_FAKE_ARGS_FILE", argsFile)
	installFakeZoxide(t, "")

	r := funcRunner{fn: func(args ...string) (string, error) {
		switch args[0] {
		case "list-sessions":
			return "", nil
		case "new-session":
			return "$1|@1|%1", nil
		case "display-message":
			return filepath.Base(dest), nil
		}
		return "", nil
	}}

	msg := startProjectCmd(r, nil, Project{Name: filepath.Base(dest), Root: dest, Zoxide: true})()
	if started, ok := msg.(projectStartedMsg); !ok || started.err != nil {
		t.Fatalf("startProjectCmd = %+v, %v", msg, ok)
	}
	if _, err := os.ReadFile(argsFile); err == nil {
		t.Error("zoxide add was called despite track mode being off")
	}
}
