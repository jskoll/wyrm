package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jskoll/wyrm/internal/config"
)

// TestMain isolates every test in this package from the developer's real
// global wyrm settings file (~/.config/wyrm/config.toml), so run()'s call
// to config.LoadSettings() always sees defaults unless a test overrides
// HOME/XDG_CONFIG_HOME itself.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "wyrm-test-config")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_CONFIG_HOME", dir)
	os.Setenv("HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// fakeRunner is a minimal tmux.Runner double, mirroring the one in
// internal/session's tests: it fabricates -P -F output for commands that
// need it and can be told to fail specific commands by name.
type fakeRunner struct {
	calls [][]string
	seq   int
	fail  map[string]bool
	// listOutput is returned verbatim for any "list-sessions" call: it backs
	// both tmux.FindSessionID's existence check (id|name pairs) and -list's
	// picker.ListSessions (the fuller id|windows|attached|activity|name
	// format) — whichever a given test actually exercises. Empty means "no
	// matching/running sessions", matching real tmux's "no server running".
	listOutput string
}

func (f *fakeRunner) Run(args ...string) (string, error) {
	f.calls = append(f.calls, args)
	if f.fail[args[0]] {
		return "boom", errors.New("exit status 1")
	}
	switch args[0] {
	case "new-session":
		f.seq++
		return fmt.Sprintf("$%d|@%d|%%%d", f.seq, f.seq, f.seq), nil
	case "list-sessions":
		return f.listOutput, nil
	}
	return "", nil
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wyrm.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeFakeEditor writes a shell script that overwrites its target path
// (passed as $1, wyrm's -edit convention) with content, and returns its path
// for use as $EDITOR.
func writeFakeEditor(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-editor.sh")
	script := "#!/bin/sh\ncat > \"$1\" <<'WYRM_EOF'\n" + content + "\nWYRM_EOF\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Fatal(err)
		}
	})
}

const validConfig = `
[session]
name = "proj"
root = "/tmp/proj"
[[windows]]
name = "w"
`

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-version"}, &stdout, &stderr, &fakeRunner{}, func() bool { return false }, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "wyrm "+version) {
		t.Errorf("stdout = %q, want containing version string", stdout.String())
	}
}

func TestRunFlagParseError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-bogus-flag"}, &stdout, &stderr, &fakeRunner{}, func() bool { return false }, nil)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

func TestRunConfigLoadError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	missing := filepath.Join(t.TempDir(), "nope.toml")
	code := run([]string{"-config", missing}, &stdout, &stderr, &fakeRunner{}, func() bool { return false }, nil)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "wyrm:") {
		t.Errorf("stderr = %q, want a wyrm: prefixed error", stderr.String())
	}
}

func TestRunValidateValid(t *testing.T) {
	path := writeConfig(t, validConfig)

	var stdout, stderr bytes.Buffer
	code := run([]string{"-validate", "-config", path}, &stdout, &stderr, &fakeRunner{}, func() bool { return false }, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "config valid: "+path) {
		t.Errorf("stdout = %q, want it to name the validated path", stdout.String())
	}
}

func TestRunValidateInvalid(t *testing.T) {
	path := writeConfig(t, "not valid toml [[[")

	var stdout, stderr bytes.Buffer
	code := run([]string{"-validate", "-config", path}, &stdout, &stderr, &fakeRunner{}, func() bool { return false }, nil)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "wyrm:") {
		t.Errorf("stderr = %q, want a wyrm: prefixed error", stderr.String())
	}
}

func TestRunValidateFallsBackToBuiltInDefault(t *testing.T) {
	chdir(t, t.TempDir())

	var stdout, stderr bytes.Buffer
	code := run([]string{"-validate"}, &stdout, &stderr, &fakeRunner{}, func() bool { return false }, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "config valid: built-in default") {
		t.Errorf("stdout = %q, want it to name the built-in default", stdout.String())
	}
}

func TestRunEditExisting(t *testing.T) {
	chdir(t, t.TempDir())
	if err := os.WriteFile(config.DefaultFileName, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDITOR", writeFakeEditor(t, validConfig))

	var stdout, stderr bytes.Buffer
	code := run([]string{"-edit"}, &stdout, &stderr, &fakeRunner{}, func() bool { return false }, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	got, err := os.ReadFile(config.DefaultFileName)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `name = "proj"`) {
		t.Errorf("file content = %q, want the fake editor's written content", got)
	}
	if stderr.String() != "" {
		t.Errorf("stderr = %q, want no warning for a valid save", stderr.String())
	}
}

func TestRunEditCreatesLocal(t *testing.T) {
	chdir(t, t.TempDir())
	t.Setenv("EDITOR", writeFakeEditor(t, validConfig))

	var stdout, stderr bytes.Buffer
	code := run([]string{"-edit"}, &stdout, &stderr, &fakeRunner{}, func() bool { return false }, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if _, err := os.Stat(config.DefaultFileName); err != nil {
		t.Errorf("expected %s to be created: %v", config.DefaultFileName, err)
	}
}

func TestRunEditCreatesShared(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	projectDir := t.TempDir()
	chdir(t, projectDir)

	sharedDir := t.TempDir()
	settingsDir := filepath.Join(home, ".config", "wyrm")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsContent := "storage = \"shared\"\nshared_dir = \"" + sharedDir + "\"\n"
	if err := os.WriteFile(filepath.Join(settingsDir, config.SettingsFileName), []byte(settingsContent), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDITOR", writeFakeEditor(t, validConfig))

	var stdout, stderr bytes.Buffer
	code := run([]string{"-edit"}, &stdout, &stderr, &fakeRunner{}, func() bool { return false }, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	wantPath := filepath.Join(sharedDir, filepath.Base(projectDir)+config.DefaultFileName)
	if _, err := os.Stat(wantPath); err != nil {
		t.Errorf("expected %s to be created: %v", wantPath, err)
	}
}

func TestRunEditWarnsOnInvalidSave(t *testing.T) {
	chdir(t, t.TempDir())
	t.Setenv("EDITOR", writeFakeEditor(t, "not valid toml [[["))

	var stdout, stderr bytes.Buffer
	code := run([]string{"-edit"}, &stdout, &stderr, &fakeRunner{}, func() bool { return false }, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (warn, don't fail): stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "warning:") {
		t.Errorf("stderr = %q, want a warning about the invalid save", stderr.String())
	}
}

func TestRunListTable(t *testing.T) {
	r := &fakeRunner{listOutput: strings.Join([]string{
		"$1|3|1|2000|alpha",
		"$2|1|0|1000|beta",
	}, "\n")}

	var stdout, stderr bytes.Buffer
	code := run([]string{"-list"}, &stdout, &stderr, r, func() bool { return false }, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "3 windows") || !strings.Contains(out, "(attached)") {
		t.Errorf("stdout = %q, want alpha row with window count and attached marker", out)
	}
	if !strings.Contains(out, "beta") || !strings.Contains(out, "1 window") {
		t.Errorf("stdout = %q, want beta row with singular window", out)
	}
	if strings.Index(out, "alpha") > strings.Index(out, "beta") {
		t.Errorf("stdout = %q, want alpha (more recent) before beta", out)
	}
}

func TestRunListTableEmpty(t *testing.T) {
	r := &fakeRunner{listOutput: "no server running on /tmp/tmux-1000/default"}

	var stdout, stderr bytes.Buffer
	code := run([]string{"-list"}, &stdout, &stderr, r, func() bool { return false }, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if stdout.String() != "" {
		t.Errorf("stdout = %q, want empty output for an empty session list", stdout.String())
	}
	if !strings.Contains(stderr.String(), "no running tmux sessions") {
		t.Errorf("stderr = %q, want a no-sessions notice", stderr.String())
	}
}

func TestRunListJSON(t *testing.T) {
	r := &fakeRunner{listOutput: "$1|2|1|1000|alpha"}

	var stdout, stderr bytes.Buffer
	code := run([]string{"-list", "-format", "json"}, &stdout, &stderr, r, func() bool { return false }, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	var got []struct {
		Name     string `json:"name"`
		Windows  int    `json:"windows"`
		Attached bool   `json:"attached"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal: %v (stdout=%q)", err, stdout.String())
	}
	if len(got) != 1 || got[0].Name != "alpha" || got[0].Windows != 2 || !got[0].Attached {
		t.Errorf("got %+v, want one alpha session with 2 windows, attached", got)
	}
}

func TestRunListJSONEmpty(t *testing.T) {
	r := &fakeRunner{listOutput: "no server running on /tmp/tmux-1000/default"}

	var stdout, stderr bytes.Buffer
	code := run([]string{"-list", "-format", "json"}, &stdout, &stderr, r, func() bool { return false }, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "[]" {
		t.Errorf("stdout = %q, want []", stdout.String())
	}
}

func TestRunListTOML(t *testing.T) {
	r := &fakeRunner{listOutput: "$1|2|0|1000|alpha"}

	var stdout, stderr bytes.Buffer
	code := run([]string{"-list", "-format", "toml"}, &stdout, &stderr, r, func() bool { return false }, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "name = 'alpha'") && !strings.Contains(stdout.String(), `name = "alpha"`) {
		t.Errorf("stdout = %q, want a sessions entry named alpha", stdout.String())
	}
}

func TestRunListUnknownFormat(t *testing.T) {
	r := &fakeRunner{listOutput: "$1|2|0|1000|alpha"}

	var stdout, stderr bytes.Buffer
	code := run([]string{"-list", "-format", "yaml"}, &stdout, &stderr, r, func() bool { return false }, nil)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "unknown -format") {
		t.Errorf("stderr = %q, want an unknown-format error", stderr.String())
	}
}

func TestRunDiscoverFallsBackToDefault(t *testing.T) {
	chdir(t, t.TempDir())

	var stdout, stderr bytes.Buffer
	r := &fakeRunner{}
	attachCalled := ""
	attach := func(name string) error { attachCalled = name; return nil }

	code := run(nil, &stdout, &stderr, r, func() bool { return false }, attach)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if attachCalled == "" {
		t.Error("attach was not called; want the default config's session to be created and attached")
	}
}

func TestRunDiscoverFallsBackToUserDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	chdir(t, t.TempDir())

	dir := filepath.Join(home, ".config", "wyrm")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "[session]\nname = \"user-default\"\nroot = \".\"\n[[windows]]\nname = \"w\"\n"
	if err := os.WriteFile(filepath.Join(dir, "default.wyrm.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	r := &fakeRunner{}
	code := run(nil, &stdout, &stderr, r, func() bool { return false }, func(string) error { return nil })
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "created session user-default") {
		t.Errorf("stdout = %q, want the user default config's session name", stdout.String())
	}
}

func TestRunCreateAndAttach(t *testing.T) {
	path := writeConfig(t, validConfig)

	var stdout, stderr bytes.Buffer
	r := &fakeRunner{}
	attachCalled := ""
	attach := func(name string) error { attachCalled = name; return nil }

	code := run([]string{"-config", path}, &stdout, &stderr, r, func() bool { return false }, attach)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "created session proj") {
		t.Errorf("stdout = %q, want mention of created session", stdout.String())
	}
	if attachCalled != "$1" {
		t.Errorf("attach called with %q, want the session's tmux ID $1", attachCalled)
	}
}

func TestRunCreateErrorPropagates(t *testing.T) {
	path := writeConfig(t, validConfig)

	var stdout, stderr bytes.Buffer
	r := &fakeRunner{fail: map[string]bool{"new-session": true}}

	code := run([]string{"-config", path}, &stdout, &stderr, r, func() bool { return false }, nil)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "wyrm:") {
		t.Errorf("stderr = %q, want a wyrm: prefixed error", stderr.String())
	}
}

func TestRunAttachError(t *testing.T) {
	path := writeConfig(t, validConfig)

	var stdout, stderr bytes.Buffer
	r := &fakeRunner{}
	attach := func(_ string) error { return errors.New("boom") }

	code := run([]string{"-config", path}, &stdout, &stderr, r, func() bool { return false }, attach)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "attaching to session") {
		t.Errorf("stderr = %q, want attach failure message", stderr.String())
	}
}

func TestRunInsideTmuxSwitchesClient(t *testing.T) {
	path := writeConfig(t, validConfig)

	var stdout, stderr bytes.Buffer
	r := &fakeRunner{}

	code := run([]string{"-config", path}, &stdout, &stderr, r, func() bool { return true }, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	found := false
	for _, c := range r.calls {
		if len(c) > 0 && c[0] == "switch-client" {
			found = true
			if len(c) < 3 || c[1] != "-t" || c[2] != "$1" {
				t.Errorf("switch-client args = %v, want -t targeting the session's tmux ID $1", c)
			}
		}
	}
	if !found {
		t.Error("switch-client was not called when inside tmux")
	}
}

// TestRunInsideTmuxSwitchesClientNameWithDot guards against the bug where a
// session name containing "." (e.g. "wyrm.vim") got misparsed by tmux's -t
// target syntax, which treats "." as the window.pane separator: switching
// or attaching must always target the session by its tmux ID, never by the
// raw (possibly dotted) name, regardless of what the session is called.
func TestRunInsideTmuxSwitchesClientNameWithDot(t *testing.T) {
	path := writeConfig(t, `
[session]
name = "wyrm.vim"
root = "/tmp/proj"
[[windows]]
name = "w"
`)

	var stdout, stderr bytes.Buffer
	r := &fakeRunner{}

	code := run([]string{"-config", path}, &stdout, &stderr, r, func() bool { return true }, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	found := false
	for _, c := range r.calls {
		for i, arg := range c {
			if i > 0 && c[i-1] == "-t" && arg == "wyrm.vim" {
				t.Errorf("call %v targets the raw dotted name instead of a session ID", c)
			}
		}
		if len(c) > 0 && c[0] == "switch-client" {
			found = true
			if len(c) < 3 || c[2] != "$1" {
				t.Errorf("switch-client args = %v, want -t targeting the session's tmux ID $1", c)
			}
		}
	}
	if !found {
		t.Error("switch-client was not called when inside tmux")
	}
}

func TestRunInsideTmuxSwitchClientError(t *testing.T) {
	path := writeConfig(t, validConfig)

	var stdout, stderr bytes.Buffer
	r := &fakeRunner{fail: map[string]bool{"switch-client": true}}

	code := run([]string{"-config", path}, &stdout, &stderr, r, func() bool { return true }, nil)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "switching to session") {
		t.Errorf("stderr = %q, want switch failure message", stderr.String())
	}
}

func TestRunAlreadyRunningAttaches(t *testing.T) {
	path := writeConfig(t, validConfig)

	var stdout, stderr bytes.Buffer
	r := &fakeRunner{listOutput: "$5|proj"}
	attachCalled := ""
	attach := func(name string) error { attachCalled = name; return nil }

	code := run([]string{"-config", path}, &stdout, &stderr, r, func() bool { return false }, attach)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "already running, attaching") {
		t.Errorf("stdout = %q, want already-running message", stdout.String())
	}
	if attachCalled != "$5" {
		t.Errorf("attach called with %q, want the existing session's tmux ID $5", attachCalled)
	}
}

func TestRunMigrateConfig(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", "")

		projectDir := t.TempDir()
		chdir(t, projectDir)
		if err := os.WriteFile(".wyrm.toml", []byte(validConfig), 0o644); err != nil {
			t.Fatal(err)
		}

		var stdout, stderr bytes.Buffer
		code := run([]string{"-migrate-config"}, &stdout, &stderr, &fakeRunner{}, func() bool { return false }, nil)
		if code != 0 {
			t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
		}

		want := filepath.Join(home, ".config", "wyrm", "settings", filepath.Base(projectDir)+".wyrm.toml")
		if _, err := os.Stat(want); err != nil {
			t.Errorf("expected migrated file at %s: %v", want, err)
		}
		if _, err := os.Stat(".wyrm.toml"); !os.IsNotExist(err) {
			t.Errorf("local .wyrm.toml still present after migration")
		}
		if !strings.Contains(stdout.String(), "moved") {
			t.Errorf("stdout = %q, want mention of moved file", stdout.String())
		}
		if !strings.Contains(stdout.String(), "note:") {
			t.Errorf("stdout = %q, want a note about enabling shared storage", stdout.String())
		}
	})

	t.Run("no local config", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", "")
		chdir(t, t.TempDir())

		var stdout, stderr bytes.Buffer
		code := run([]string{"-migrate-config"}, &stdout, &stderr, &fakeRunner{}, func() bool { return false }, nil)
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "no local config") {
			t.Errorf("stderr = %q, want no-local-config message", stderr.String())
		}
	})

	t.Run("destination already exists", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", "")

		projectDir := t.TempDir()
		chdir(t, projectDir)
		if err := os.WriteFile(".wyrm.toml", []byte(validConfig), 0o644); err != nil {
			t.Fatal(err)
		}

		sharedDir := filepath.Join(home, ".config", "wyrm", "settings")
		if err := os.MkdirAll(sharedDir, 0o755); err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(sharedDir, filepath.Base(projectDir)+".wyrm.toml")
		if err := os.WriteFile(dst, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}

		var stdout, stderr bytes.Buffer
		code := run([]string{"-migrate-config"}, &stdout, &stderr, &fakeRunner{}, func() bool { return false }, nil)
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "already exists") {
			t.Errorf("stderr = %q, want already-exists message", stderr.String())
		}
	})
}

func TestRunKill(t *testing.T) {
	path := writeConfig(t, validConfig)

	t.Run("success", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		r := &fakeRunner{listOutput: "$5|proj"}
		code := run([]string{"-config", path, "-kill"}, &stdout, &stderr, r, func() bool { return false }, nil)
		if code != 0 {
			t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "killed session proj") {
			t.Errorf("stdout = %q, want killed session message", stdout.String())
		}
	})

	t.Run("not running", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		r := &fakeRunner{}
		code := run([]string{"-config", path, "-kill"}, &stdout, &stderr, r, func() bool { return false }, nil)
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "wyrm:") {
			t.Errorf("stderr = %q, want a wyrm: prefixed error", stderr.String())
		}
	})
}
