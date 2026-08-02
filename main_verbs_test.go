package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jskoll/wyrm/internal/config"
)

// writeLocalConfig drops a .wyrm.toml in a fresh directory and chdir's there,
// so discovery finds it the way it would for a real project.
func writeLocalConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, config.DefaultFileName)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	chdir(t, dir)
	return path
}

const localConfig = `
[session]
name = "proj"
root = "."

[[windows]]
name = "w"
`

// TestRunReportsConfigSource: runUp discarded the resolved source, so with
// five discovery layers a user who got an unexpected session had no way to
// find out which config produced it.
func TestRunReportsConfigSource(t *testing.T) {
	path := writeLocalConfig(t, localConfig)

	var stdout, stderr bytes.Buffer
	code := run(nil, &stdout, &stderr, &fakeRunner{}, func() bool { return false }, func(string) error { return nil })
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "using config") {
		t.Errorf("stderr = %q, want the resolved config reported", stderr.String())
	}
	if !strings.Contains(stderr.String(), filepath.Base(path)) {
		t.Errorf("stderr = %q, want it to name %s", stderr.String(), path)
	}
}

// TestRunUpDryRun prints the plan without touching tmux — worth having for a
// tool whose whole job is running shell commands out of a file.
func TestRunUpDryRun(t *testing.T) {
	writeLocalConfig(t, localConfig+"\n  [[windows.splits]]\n  command = \"nvim\"\n")

	r := &fakeRunner{}
	var stdout, stderr bytes.Buffer
	code := run([]string{"up", "-n"}, &stdout, &stderr, r, func() bool { return false }, func(string) error {
		t.Error("dry run must not attach")
		return nil
	})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"dry run", "tmux new-session", "send-keys"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout = %q, want it to contain %q", out, want)
		}
	}
	if len(r.calls) != 0 {
		t.Errorf("dry run issued real tmux commands: %v", r.calls)
	}
}

// TestRunUpDryRunDoesNotExecuteHooks: `wyrm up -n` used to run
// on_project_start for real. Its entire purpose is reading a config's shell
// before it runs, and the hook is the part most worth reading first — a
// recording tmux.Runner covers the tmux commands, but hooks never go through
// the Runner, so nothing stopped them.
func TestRunUpDryRunDoesNotExecuteHooks(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "hook-ran")
	cfg := "[session]\nname = \"proj\"\nroot = \".\"\non_project_start = \"touch " +
		marker + "\"\non_project_exit = \"touch " + marker + "\"\n\n[[windows]]\nname = \"w\"\n"
	writeLocalConfig(t, cfg)

	var stdout, stderr bytes.Buffer
	code := run([]string{"up", "-n"}, &stdout, &stderr, &fakeRunner{},
		func() bool { return false }, func(string) error { return nil })
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("dry run executed on_project_start")
	}
	if !strings.Contains(stdout.String(), "would run on_project_start") {
		t.Errorf("stdout = %q, want the hook described rather than run", stdout.String())
	}
}

// TestRunUpFirstStartThenRestartFiresCorrectHook is the CLI-level version of
// internal/session's history test: a project's genuine first `wyrm up` fires
// on_project_first_start, and a later `wyrm restart` of the same project
// (real persistent state, not a same-process approximation) fires
// on_project_restart instead — proving state.Load/MarkStarted are actually
// wired into the up/restart command paths, not just Create's own signature.
func TestRunUpFirstStartThenRestartFiresCorrectHook(t *testing.T) {
	cfg := "\n[session]\nname = \"proj\"\nroot = \".\"\n" +
		"on_project_first_start = \"true\"\non_project_restart = \"true\"\n\n" +
		"[[windows]]\nname = \"w\"\n"
	writeLocalConfig(t, cfg)

	var stdout, stderr bytes.Buffer
	code := run([]string{"up"}, &stdout, &stderr, &fakeRunner{}, func() bool { return false }, func(string) error { return nil })
	if code != 0 {
		t.Fatalf("up: exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "running on_project_first_start") {
		t.Errorf("up stderr = %q, want on_project_first_start to run", stderr.String())
	}

	// restart kills (no session was ever really created by the fakeRunner, so
	// "nothing to stop" is expected) and rebuilds — by now the project is
	// recorded as started, so this build should fire restart, not first_start.
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"restart"}, &stdout, &stderr, &fakeRunner{}, func() bool { return false }, func(string) error { return nil })
	if code != 0 {
		t.Fatalf("restart: exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "running on_project_restart") {
		t.Errorf("restart stderr = %q, want on_project_restart to run", stderr.String())
	}
	if strings.Contains(stderr.String(), "on_project_first_start") {
		t.Errorf("restart stderr = %q, want on_project_first_start NOT to run again", stderr.String())
	}
}

// TestRunKillDryRunDoesNotExecuteHooks is the teardown half. Unlike a build,
// the session lookup is genuinely performed — it names what would be killed —
// so this also checks the kill-session itself is withheld.
func TestRunKillDryRunDoesNotExecuteHooks(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "hook-ran")
	cfg := "[session]\nname = \"proj\"\nroot = \".\"\non_project_exit = \"touch " +
		marker + "\"\n\n[[windows]]\nname = \"w\"\n"
	writeLocalConfig(t, cfg)

	r := &fakeRunner{listOutput: "$7|proj"}
	var stdout, stderr bytes.Buffer
	code := run([]string{"kill", "-n"}, &stdout, &stderr, r,
		func() bool { return false }, func(string) error { return nil })
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("dry run executed on_project_exit")
	}
	out := stdout.String()
	if !strings.Contains(out, "would run on_project_exit") {
		t.Errorf("stdout = %q, want the hook described rather than run", out)
	}
	if !strings.Contains(out, "kill-session -t $7") {
		t.Errorf("stdout = %q, want the kill described against the resolved id", out)
	}
	for _, c := range r.calls {
		if len(c) > 0 && c[0] == "kill-session" {
			t.Errorf("dry run really killed the session: %v", r.calls)
		}
	}
}

// TestRunRestartKillsThenRebuilds: "I edited the config, make the session
// match it" previously had to be spelled `wyrm kill && wyrm`.
func TestRunRestartKillsThenRebuilds(t *testing.T) {
	writeLocalConfig(t, localConfig)

	r := &fakeRunner{listOutput: "$1|proj"}
	var stdout, stderr bytes.Buffer
	attached := ""
	code := run([]string{"restart"}, &stdout, &stderr, r, func() bool { return false }, func(id string) error {
		attached = id
		return nil
	})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	joined := joinCalls(r)
	if !strings.Contains(joined, "kill-session -t $1") {
		t.Errorf("restart did not kill the running session:\n%s", joined)
	}
	if !strings.Contains(joined, "new-session") {
		t.Errorf("restart did not rebuild the session:\n%s", joined)
	}
	if attached == "" {
		t.Error("restart should attach to the rebuilt session")
	}
}

// TestRunRestartWithNothingRunningStillBuilds: restart means "end up with a
// freshly built session", which is satisfiable whether or not one was running.
func TestRunRestartWithNothingRunningStillBuilds(t *testing.T) {
	writeLocalConfig(t, localConfig)

	r := &fakeRunner{}
	var stdout, stderr bytes.Buffer
	code := run([]string{"restart"}, &stdout, &stderr, r, func() bool { return false }, func(string) error { return nil })
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(joinCalls(r), "new-session") {
		t.Errorf("restart did not build the session:\n%s", joinCalls(r))
	}
	if !strings.Contains(stderr.String(), "nothing to stop") {
		t.Errorf("stderr = %q, want a note that there was nothing to stop", stderr.String())
	}
}

// TestRunRestartDetachSkipsAttach guards `wyrm restart -d`: the session
// still gets torn down and rebuilt for real, but attach must never be called.
func TestRunRestartDetachSkipsAttach(t *testing.T) {
	writeLocalConfig(t, localConfig)

	r := &fakeRunner{listOutput: "$1|proj"}
	var stdout, stderr bytes.Buffer
	attachCalled := false
	code := run([]string{"restart", "-d"}, &stdout, &stderr, r, func() bool { return false }, func(string) error {
		attachCalled = true
		return nil
	})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	joined := joinCalls(r)
	if !strings.Contains(joined, "kill-session -t $1") {
		t.Errorf("restart -d did not kill the running session:\n%s", joined)
	}
	if !strings.Contains(joined, "new-session") {
		t.Errorf("restart -d did not rebuild the session:\n%s", joined)
	}
	if attachCalled {
		t.Error("restart -d attached; want it to skip attaching")
	}
}

// TestRunKillByName: `wyrm <name>` attached by name but `wyrm kill` could only
// kill the current folder's session — an arbitrary asymmetry.
func TestRunKillByName(t *testing.T) {
	chdir(t, t.TempDir())
	r := &fakeRunner{listOutput: "$7|other"}

	var stdout, stderr bytes.Buffer
	code := run([]string{"kill", "other"}, &stdout, &stderr, r, func() bool { return false }, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(joinCalls(r), "kill-session -t $7") {
		t.Errorf("kill by name did not target the session's id:\n%s", joinCalls(r))
	}
	if !strings.Contains(stdout.String(), "killed session other") {
		t.Errorf("stdout = %q, want a killed-session message", stdout.String())
	}
}

func TestRunKillByNameNotFound(t *testing.T) {
	chdir(t, t.TempDir())
	var stdout, stderr bytes.Buffer
	code := run([]string{"kill", "ghost"}, &stdout, &stderr, &fakeRunner{}, func() bool { return false }, nil)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "no running session") {
		t.Errorf("stderr = %q, want a not-found message", stderr.String())
	}
}

func TestRunKillRejectsNameAndConfigTogether(t *testing.T) {
	chdir(t, t.TempDir())
	var stdout, stderr bytes.Buffer
	code := run([]string{"kill", "-config", "x.toml", "name"}, &stdout, &stderr, &fakeRunner{}, func() bool { return false }, nil)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

// TestRunNameStartsKnownProject is what makes shared storage worth using:
// without it, centralizing configs bought nothing, because the only way to
// start one was still to cd into its folder first.
func TestRunNameStartsKnownProject(t *testing.T) {
	writeLocalConfig(t, localConfig)

	r := &fakeRunner{} // nothing running
	var stdout, stderr bytes.Buffer
	attached := ""
	code := run([]string{"proj"}, &stdout, &stderr, r, func() bool { return false }, func(id string) error {
		attached = id
		return nil
	})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(joinCalls(r), "new-session") {
		t.Errorf("a known project name did not start its session:\n%s", joinCalls(r))
	}
	if attached == "" {
		t.Error("starting a project by name should attach to it")
	}
}

// TestRunNameStartsSharedProject covers the same path for a config in the
// shared directory, which is where it actually matters.
func TestRunNameStartsSharedProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	sharedDir := filepath.Join(home, ".config", "wyrm", "settings")
	if err := os.MkdirAll(sharedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projectDir := t.TempDir()
	content := "[session]\nname = \"shared-proj\"\nroot = \"" + projectDir + "\"\n\n[[windows]]\nname = \"w\"\n"
	if err := os.WriteFile(filepath.Join(sharedDir, "shared-proj.wyrm.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	chdir(t, t.TempDir()) // stand somewhere unrelated

	r := &fakeRunner{}
	var stdout, stderr bytes.Buffer
	code := run([]string{"shared-proj"}, &stdout, &stderr, r, func() bool { return false }, func(string) error { return nil })
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	joined := joinCalls(r)
	if !strings.Contains(joined, "new-session") {
		t.Errorf("shared project was not started:\n%s", joined)
	}
	if !strings.Contains(joined, projectDir) {
		t.Errorf("session was not rooted at the project directory %q:\n%s", projectDir, joined)
	}
}

// TestRunListRejectsPositionalArg: `wyrm list json` looks like it should work
// and used to silently print the default table instead.
func TestRunListRejectsPositionalArg(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"list", "json"}, &stdout, &stderr, &fakeRunner{}, func() bool { return false }, nil)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unexpected argument") {
		t.Errorf("stderr = %q, want an unexpected-argument error", stderr.String())
	}
}

// TestRunUpWarnsAboutLikelyConfigMistakes surfaces the non-fatal diagnostics
// at the point they matter.
func TestRunUpWarnsAboutLikelyConfigMistakes(t *testing.T) {
	writeLocalConfig(t, `
[session]
name = "proj"
root = "."

[[windows]]
name = "w"
layout = "tiled"

  [[windows.splits]]
  type = "h"
  command = "nvim"
`)
	var stdout, stderr bytes.Buffer
	run([]string{"validate"}, &stdout, &stderr, &fakeRunner{}, func() bool { return false }, nil)
	if !strings.Contains(stderr.String(), "warning") {
		t.Errorf("stderr = %q, want config warnings", stderr.String())
	}
}

func joinCalls(r *fakeRunner) string {
	lines := make([]string, len(r.calls))
	for i, c := range r.calls {
		lines[i] = strings.Join(c, " ")
	}
	return strings.Join(lines, "\n")
}
