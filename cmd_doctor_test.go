package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jskoll/wyrm/internal/config"
	"github.com/jskoll/wyrm/internal/tmux"
)

// doctorEnv points every XDG lookup at a fresh directory and returns it, so a
// test can plant a settings/theme file without touching the developer's own.
func doctorEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	return dir
}

func writeGlobalSettings(t *testing.T, body string) {
	t.Helper()
	path, err := config.SettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runDoctor drives `wyrm doctor` with a tmux double and returns its output.
func runDoctor(t *testing.T, r tmux.Runner, args ...string) (string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(append([]string{"doctor"}, args...), &stdout, &stderr, r,
		func() bool { return false }, nil)
	return stdout.String(), code
}

// tmuxVersionRunner answers `tmux -V` so the version check has something to
// read; everything else behaves like the shared fakeRunner.
type tmuxVersionRunner struct {
	fakeRunner
	version string
}

func (r *tmuxVersionRunner) Run(args ...string) (string, error) {
	if len(args) == 1 && args[0] == "-V" {
		r.calls = append(r.calls, args)
		return "tmux " + r.version, nil
	}
	return r.fakeRunner.Run(args...)
}

// Nothing misconfigured means nothing to fix. This deliberately does not
// assert the "no problems found" summary: whether a clipboard backend or an
// $EDITOR exists is a property of the machine, and CI runners have neither, so
// a clean checkout legitimately reports warnings there. What has to hold
// everywhere is that no *error* is invented and every check is accounted for.
func TestDoctorCleanEnvironment(t *testing.T) {
	doctorEnv(t)
	chdir(t, t.TempDir())

	out, code := runDoctor(t, &fakeRunner{})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "err ") {
			t.Errorf("nothing is misconfigured, so nothing should be an error:\n%s", out)
			break
		}
	}
	// The report is the point: it has to say what it looked at even when
	// everything is fine.
	for _, want := range []string{"tmux", "settings", "storage", "config", "agent", "clipboard"} {
		if !strings.Contains(out, want) {
			t.Errorf("report is missing the %q line:\n%s", want, out)
		}
	}
}

// `tmux -V` answering with nothing rendered as " at /path (unrecognized
// version)", leading space and all.
func TestDoctorTmuxSilentVersion(t *testing.T) {
	doctorEnv(t)
	chdir(t, t.TempDir())

	out, _ := runDoctor(t, &fakeRunner{})
	if !strings.Contains(out, "reported no version") {
		t.Errorf("want the empty version named as such, got:\n%s", out)
	}
	if strings.Contains(out, "unrecognized version") {
		t.Errorf("an empty version is not an unrecognized one:\n%s", out)
	}
}

// A settings file that will not parse is reported rather than crashing the
// command, and the rest of the report still runs on the defaults.
func TestDoctorUnparseableSettings(t *testing.T) {
	doctorEnv(t)
	writeGlobalSettings(t, "storage = \n")
	chdir(t, t.TempDir())

	out, code := runDoctor(t, &fakeRunner{})
	if code != 1 {
		t.Errorf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "err ") || !strings.Contains(out, "settings") {
		t.Errorf("want a settings error, got:\n%s", out)
	}
	if !strings.Contains(out, "clipboard") {
		t.Errorf("checks after settings should still run:\n%s", out)
	}
}

// An unknown key is silently ignored by the loader, which is precisely why it
// needs surfacing: the feature it was meant to configure simply never happens.
func TestDoctorReportsIgnoredSettingsKeys(t *testing.T) {
	doctorEnv(t)
	writeGlobalSettings(t, "[tui]\nmosue = true\n")
	chdir(t, t.TempDir())

	out, code := runDoctor(t, &fakeRunner{})
	if code != 0 {
		t.Errorf("an ignored key is a warning, not an error: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "tui.mosue") {
		t.Errorf("want the ignored key named, got:\n%s", out)
	}
	if _, strictCode := runDoctor(t, &fakeRunner{}, "-strict"); strictCode != 1 {
		t.Errorf("-strict exit = %d, want 1", strictCode)
	}
}

// A busy_pattern that does not compile disables the agent markers exactly as
// thoroughly as turning the feature off, and says nothing while doing it.
func TestDoctorReportsBrokenAgentProfile(t *testing.T) {
	doctorEnv(t)
	writeGlobalSettings(t, "[[tui.agent.profiles]]\ncommands = [\"aider\"]\nbusy_pattern = \"(unclosed\"\n")
	chdir(t, t.TempDir())

	out, code := runDoctor(t, &fakeRunner{})
	if code != 1 {
		t.Errorf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "busy_pattern") {
		t.Errorf("want the offending pattern named, got:\n%s", out)
	}
	// The notify check must not be swallowed by the profile failure.
	if !strings.Contains(out, "agent notify") {
		t.Errorf("notify check should still run after a profile error:\n%s", out)
	}
}

func TestDoctorWildcards(t *testing.T) {
	t.Run("pattern matching nothing warns", func(t *testing.T) {
		dir := doctorEnv(t)
		tmpl := filepath.Join(dir, "tmpl.wyrm.toml")
		if err := os.WriteFile(tmpl, []byte("[session]\nroot = \".\"\n\n[[windows]]\nname = \"w\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		writeGlobalSettings(t, "[[wildcard]]\npattern = \""+filepath.Join(dir, "nothing-here", "*")+"\"\nconfig = \""+tmpl+"\"\n")
		chdir(t, t.TempDir())

		out, code := runDoctor(t, &fakeRunner{})
		if code != 0 {
			t.Errorf("a pattern matching nothing is a warning: exit %d\n%s", code, out)
		}
		if !strings.Contains(out, "matches no directories") {
			t.Errorf("want the empty match reported, got:\n%s", out)
		}
	})

	t.Run("unloadable template errors", func(t *testing.T) {
		dir := doctorEnv(t)
		writeGlobalSettings(t, "[[wildcard]]\npattern = \""+filepath.Join(dir, "*")+"\"\nconfig = \""+filepath.Join(dir, "missing.toml")+"\"\n")
		chdir(t, t.TempDir())

		out, code := runDoctor(t, &fakeRunner{})
		if code != 1 {
			t.Errorf("exit code = %d, want 1\n%s", code, out)
		}
		if !strings.Contains(out, "wildcard[0]") {
			t.Errorf("want the wildcard index named, got:\n%s", out)
		}
	})

	t.Run("matching pattern is ok", func(t *testing.T) {
		dir := doctorEnv(t)
		if err := os.MkdirAll(filepath.Join(dir, "projects", "alpha"), 0o755); err != nil {
			t.Fatal(err)
		}
		tmpl := filepath.Join(dir, "tmpl.wyrm.toml")
		if err := os.WriteFile(tmpl, []byte("[session]\nroot = \".\"\n\n[[windows]]\nname = \"w\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		writeGlobalSettings(t, "[[wildcard]]\npattern = \""+filepath.Join(dir, "projects", "*")+"\"\nconfig = \""+tmpl+"\"\n")
		chdir(t, t.TempDir())

		out, code := runDoctor(t, &fakeRunner{})
		if code != 0 {
			t.Errorf("exit code = %d, want 0\n%s", code, out)
		}
		if !strings.Contains(out, "1 directory") {
			t.Errorf("want the match count, got:\n%s", out)
		}
	})
}

// The README requires tmux 3.1+ because `split-window -l N%` — how every
// `size` in a config is applied — needs it. Nothing checked, so an old tmux
// produced a silently even layout.
func TestDoctorTmuxVersion(t *testing.T) {
	for _, tc := range []struct {
		version  string
		wantWarn bool
	}{
		{"3.7c", false},
		{"3.1", false},
		{"3.0a", true},
		{"2.9", true},
	} {
		t.Run(tc.version, func(t *testing.T) {
			doctorEnv(t)
			chdir(t, t.TempDir())
			out, _ := runDoctor(t, &tmuxVersionRunner{version: tc.version})
			warned := strings.Contains(out, "wyrm needs 3.1 or newer")
			if warned != tc.wantWarn {
				t.Errorf("tmux %s: warned = %v, want %v\n%s", tc.version, warned, tc.wantWarn, out)
			}
		})
	}
}

func TestParseTmuxVersion(t *testing.T) {
	for _, tc := range []struct {
		in           string
		major, minor int
		ok           bool
	}{
		{"3.1", 3, 1, true},
		// tmux appends a letter to patch releases; a plain numeric split reads
		// "3.5a" as 3.0 and reports a current tmux as too old.
		{"3.5a", 3, 5, true},
		{"3.7c", 3, 7, true},
		{"next-3.6", 3, 6, true},
		{"2.9", 2, 9, true},
		{"master", 0, 0, false},
		{"", 0, 0, false},
		{"3", 0, 0, false},
	} {
		major, minor, ok := parseTmuxVersion(tc.in)
		if major != tc.major || minor != tc.minor || ok != tc.ok {
			t.Errorf("parseTmuxVersion(%q) = %d, %d, %v; want %d, %d, %v",
				tc.in, major, minor, ok, tc.major, tc.minor, tc.ok)
		}
	}
}

func TestDoctorRejectsPositionalArgument(t *testing.T) {
	doctorEnv(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"doctor", "everything"}, &stdout, &stderr, &fakeRunner{},
		func() bool { return false }, nil)
	if code != 2 {
		t.Errorf("exit code = %d, want 2 (usage error)", code)
	}
}

func TestPlural(t *testing.T) {
	for _, tc := range []struct {
		n    int
		args []string
		want string
	}{
		{1, []string{"error"}, "1 error"},
		{0, []string{"error"}, "0 errors"},
		{2, []string{"error"}, "2 errors"},
		{1, []string{"directory", "directories"}, "1 directory"},
		{3, []string{"directory", "directories"}, "3 directories"},
	} {
		got := plural(tc.n, tc.args[0], tc.args[1:]...)
		if got != tc.want {
			t.Errorf("plural(%d, %v) = %q, want %q", tc.n, tc.args, got, tc.want)
		}
	}
}
