package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRunner is a minimal tmux.Runner double, mirroring the one in
// internal/session's tests: it fabricates -P -F output for commands that
// need it and can be told to fail specific commands by name.
type fakeRunner struct {
	calls      [][]string
	seq        int
	hasSession bool
	fail       map[string]bool
}

func (f *fakeRunner) Run(args ...string) (string, error) {
	f.calls = append(f.calls, args)
	if f.fail[args[0]] {
		return "boom", errors.New("exit status 1")
	}
	switch args[0] {
	case "new-session":
		f.seq++
		return fmt.Sprintf("@%d|%%%d", f.seq, f.seq), nil
	case "has-session":
		if !f.hasSession {
			return "no such session", errors.New("exit status 1")
		}
		return "", nil
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
	if attachCalled != "proj" {
		t.Errorf("attach called with %q, want proj", attachCalled)
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
	attach := func(name string) error { return errors.New("boom") }

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
	r := &fakeRunner{hasSession: true}
	attachCalled := ""
	attach := func(name string) error { attachCalled = name; return nil }

	code := run([]string{"-config", path}, &stdout, &stderr, r, func() bool { return false }, attach)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "already running, attaching") {
		t.Errorf("stdout = %q, want already-running message", stdout.String())
	}
	if attachCalled != "proj" {
		t.Errorf("attach called with %q, want proj", attachCalled)
	}
}

func TestRunKill(t *testing.T) {
	path := writeConfig(t, validConfig)

	t.Run("success", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		r := &fakeRunner{hasSession: true}
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
		r := &fakeRunner{hasSession: false}
		code := run([]string{"-config", path, "-kill"}, &stdout, &stderr, r, func() bool { return false }, nil)
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "wyrm:") {
			t.Errorf("stderr = %q, want a wyrm: prefixed error", stderr.String())
		}
	})
}
