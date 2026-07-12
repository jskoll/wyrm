package tmux

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// installFakeTmux puts a fake "tmux" script at the front of PATH. The script
// echoes its argv (trimmed for TestExecRun to check), optionally records argv
// to TMUX_FAKE_ARGS_FILE (used where stdio isn't captured, e.g. Attach), and
// exits with TMUX_FAKE_EXIT (default 0).
func installFakeTmux(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake tmux script requires a POSIX shell")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"if [ -n \"$TMUX_FAKE_ARGS_FILE\" ]; then printf '%s\\n' \"$*\" > \"$TMUX_FAKE_ARGS_FILE\"; fi\n" +
		"printf '  %s  \\n' \"$*\"\n" +
		"exit \"${TMUX_FAKE_EXIT:-0}\"\n"
	path := filepath.Join(dir, "tmux")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestInsideTmux(t *testing.T) {
	t.Setenv("TMUX", "")
	if InsideTmux() {
		t.Error("InsideTmux() = true, want false when TMUX unset")
	}
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1234,0")
	if !InsideTmux() {
		t.Error("InsideTmux() = false, want true when TMUX set")
	}
}

func TestExecRun(t *testing.T) {
	installFakeTmux(t)

	out, err := (Exec{}).Run("list-sessions")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "list-sessions" {
		t.Errorf("Run output = %q, want trimmed %q", out, "list-sessions")
	}
}

func TestExecRunSocketName(t *testing.T) {
	installFakeTmux(t)

	out, err := (Exec{SocketName: "wyrm-test"}).Run("list-sessions")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := "-L wyrm-test list-sessions"; out != want {
		t.Errorf("Run output = %q, want %q", out, want)
	}
}

func TestExecRunError(t *testing.T) {
	installFakeTmux(t)
	t.Setenv("TMUX_FAKE_EXIT", "1")

	if _, err := (Exec{}).Run("bogus"); err == nil {
		t.Error("Run with nonzero exit: want error, got nil")
	}
}

func TestAttach(t *testing.T) {
	installFakeTmux(t)
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("TMUX_FAKE_ARGS_FILE", argsFile)

	if err := Attach("myproj"); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if want := "attach-session -t myproj"; strings.TrimSpace(string(got)) != want {
		t.Errorf("Attach invoked tmux with %q, want %q", strings.TrimSpace(string(got)), want)
	}
}

func TestAttachError(t *testing.T) {
	installFakeTmux(t)
	t.Setenv("TMUX_FAKE_EXIT", "1")

	if err := Attach("myproj"); err == nil {
		t.Error("Attach with nonzero exit: want error, got nil")
	}
}
