package tmux

import (
	"errors"
	"fmt"
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

	if err := (Exec{}).Attach("myproj"); err != nil {
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

	if err := (Exec{}).Attach("myproj"); err == nil {
		t.Error("Attach with nonzero exit: want error, got nil")
	}
}

// TestAttachSocketName guards the bug fixed alongside adding real socket
// support: Attach used to be a free function that ignored SocketName
// entirely, so a session built on a named socket would attach to the
// default server instead.
func TestAttachSocketName(t *testing.T) {
	installFakeTmux(t)
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("TMUX_FAKE_ARGS_FILE", argsFile)

	if err := (Exec{SocketName: "wyrm-test"}).Attach("myproj"); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if want := "-L wyrm-test attach-session -t myproj"; strings.TrimSpace(string(got)) != want {
		t.Errorf("Attach invoked tmux with %q, want %q", strings.TrimSpace(string(got)), want)
	}
}

func TestExecCommand(t *testing.T) {
	installFakeTmux(t)
	// Rename the fake binary so a bare "tmux" lookup on PATH would fail,
	// proving Command actually selects the binary rather than PATH order
	// happening to prefer it.
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s' \"$*\"\n"
	path := filepath.Join(dir, "custom-tmux")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := (Exec{Command: path}).Run("list-sessions")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "list-sessions" {
		t.Errorf("Run output = %q, want %q", out, "list-sessions")
	}
}

// stubRunner returns canned output/err regardless of the command given.
type stubRunner struct {
	out string
	err error
}

func (s stubRunner) Run(_ ...string) (string, error) {
	return s.out, s.err
}

func TestFindSessionIDMatch(t *testing.T) {
	r := stubRunner{out: "$1|alpha\n$2|wyrm.vim"}
	id, ok, err := FindSessionID(r, "wyrm.vim")
	if err != nil {
		t.Fatalf("FindSessionID: %v", err)
	}
	if !ok || id != "$2" {
		t.Errorf("FindSessionID = %q, %v; want $2, true", id, ok)
	}
}

func TestFindSessionIDNoMatch(t *testing.T) {
	r := stubRunner{out: "$1|alpha"}
	id, ok, err := FindSessionID(r, "missing")
	if err != nil {
		t.Fatalf("FindSessionID: %v", err)
	}
	if ok {
		t.Errorf("FindSessionID = %q, %v; want not-ok", id, ok)
	}
}

func TestFindSessionIDNoServer(t *testing.T) {
	r := stubRunner{out: "no server running on /tmp/tmux-1000/default", err: errors.New("exit status 1")}
	if _, ok, err := FindSessionID(r, "alpha"); err != nil || ok {
		t.Errorf("FindSessionID with no server: ok=%v err=%v, want false, nil", ok, err)
	}
}

func TestFindSessionIDRealError(t *testing.T) {
	r := stubRunner{out: "something else broke", err: errors.New("exit status 1")}
	if _, _, err := FindSessionID(r, "alpha"); err == nil {
		t.Error("FindSessionID with a real tmux failure: want error, got nil")
	}
}

func TestCurrentSession(t *testing.T) {
	r := stubRunner{out: "$3|myproj"}
	id, name, err := CurrentSession(r)
	if err != nil {
		t.Fatalf("CurrentSession: %v", err)
	}
	if id != "$3" || name != "myproj" {
		t.Errorf("CurrentSession = %q, %q; want $3, myproj", id, name)
	}
}

func TestCurrentSessionCommandError(t *testing.T) {
	r := stubRunner{out: "no current client", err: errors.New("exit status 1")}
	if _, _, err := CurrentSession(r); err == nil {
		t.Error("CurrentSession with a failing command: want error, got nil")
	}
}

func TestCurrentSessionUnexpectedOutput(t *testing.T) {
	r := stubRunner{out: "no-separator"}
	if _, _, err := CurrentSession(r); err == nil {
		t.Error("CurrentSession with unexpected output: want error, got nil")
	}
}

func TestListWindows(t *testing.T) {
	r := stubRunner{out: "0|@1|1|abcd,1x1,0,0,0|editor\n1|@2|0|efgh,1x1,0,0,1|server\n"}
	windows, err := ListWindows(r, "$3")
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	want := []WindowInfo{
		{Index: 0, ID: "@1", Active: true, Layout: "abcd,1x1,0,0,0", Name: "editor"},
		{Index: 1, ID: "@2", Active: false, Layout: "efgh,1x1,0,0,1", Name: "server"},
	}
	if len(windows) != len(want) {
		t.Fatalf("ListWindows returned %d windows, want %d", len(windows), len(want))
	}
	for i := range want {
		if windows[i] != want[i] {
			t.Errorf("windows[%d] = %+v, want %+v", i, windows[i], want[i])
		}
	}
}

func TestListWindowsCommandError(t *testing.T) {
	r := stubRunner{out: "boom", err: errors.New("exit status 1")}
	if _, err := ListWindows(r, "$3"); err == nil {
		t.Error("ListWindows with a failing command: want error, got nil")
	}
}

func TestListWindowsMalformedLine(t *testing.T) {
	r := stubRunner{out: "not-enough-fields"}
	if _, err := ListWindows(r, "$3"); err == nil {
		t.Error("ListWindows with a malformed line: want error, got nil")
	}
}

func TestListPanes(t *testing.T) {
	r := stubRunner{out: "%0|0|1|nvim|/path/to/nvim\n%1|1|0|htop|/path/to/htop\n"}
	panes, err := ListPanes(r, "@1")
	if err != nil {
		t.Fatalf("ListPanes: %v", err)
	}
	want := []PaneInfo{
		{ID: "%0", Index: 0, Active: true, Command: "nvim", Path: "/path/to/nvim"},
		{ID: "%1", Index: 1, Active: false, Command: "htop", Path: "/path/to/htop"},
	}
	if len(panes) != len(want) {
		t.Fatalf("ListPanes returned %d panes, want %d", len(panes), len(want))
	}
	for i := range want {
		if panes[i] != want[i] {
			t.Errorf("panes[%d] = %+v, want %+v", i, panes[i], want[i])
		}
	}
}

func TestListPanesCommandError(t *testing.T) {
	r := stubRunner{out: "boom", err: errors.New("exit status 1")}
	if _, err := ListPanes(r, "@1"); err == nil {
		t.Error("ListPanes with a failing command: want error, got nil")
	}
}

func TestListAllPanes(t *testing.T) {
	r := stubRunner{out: "$1\x01webapp\x01@1\x010\x01code\x01%1\x010\x01nvim\n" +
		"$1\x01webapp\x01@2\x011\x01server\x01%2\x010\x01npm\n"}
	refs, err := ListAllPanes(r)
	if err != nil {
		t.Fatalf("ListAllPanes: %v", err)
	}
	want := []PaneRef{
		{SessionID: "$1", SessionName: "webapp", WindowID: "@1", WindowIndex: 0, WindowName: "code", PaneID: "%1", PaneIndex: 0, Command: "nvim"},
		{SessionID: "$1", SessionName: "webapp", WindowID: "@2", WindowIndex: 1, WindowName: "server", PaneID: "%2", PaneIndex: 0, Command: "npm"},
	}
	if len(refs) != len(want) {
		t.Fatalf("ListAllPanes returned %d refs, want %d", len(refs), len(want))
	}
	for i := range want {
		if refs[i] != want[i] {
			t.Errorf("refs[%d] = %+v, want %+v", i, refs[i], want[i])
		}
	}
}

// TestListAllPanesNameContainingPipe guards the reason ListAllPanes parses
// its own output instead of going through records: a session or window name
// containing a literal "|" must not corrupt the fields around it, since "|"
// is an ordinary character in a tmux name, not a reserved one.
func TestListAllPanesNameContainingPipe(t *testing.T) {
	r := stubRunner{out: "$1\x01web|app\x01@1\x010\x01code|review\x01%1\x010\x01nvim\n"}
	refs, err := ListAllPanes(r)
	if err != nil {
		t.Fatalf("ListAllPanes: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("ListAllPanes returned %d refs, want 1", len(refs))
	}
	if refs[0].SessionName != "web|app" {
		t.Errorf("SessionName = %q, want %q", refs[0].SessionName, "web|app")
	}
	if refs[0].WindowName != "code|review" {
		t.Errorf("WindowName = %q, want %q", refs[0].WindowName, "code|review")
	}
}

func TestListAllPanesCommandError(t *testing.T) {
	r := stubRunner{out: "boom", err: errors.New("exit status 1")}
	if _, err := ListAllPanes(r); err == nil {
		t.Error("ListAllPanes with a failing command: want error, got nil")
	}
}

func TestListPanesMalformedLine(t *testing.T) {
	r := stubRunner{out: "not-enough-fields"}
	if _, err := ListPanes(r, "@1"); err == nil {
		t.Error("ListPanes with a malformed line: want error, got nil")
	}
}

// TestNoServerRunningPrefersTheSentinel: "is the server down?" used to be
// answered only by matching tmux's English diagnostic, because the %v-formatted
// errors on the way up destroyed the chain. Exec.Run now wraps ErrNoServer, so
// the typed check answers first and the text match is only a fallback.
func TestNoServerRunningPrefersTheSentinel(t *testing.T) {
	tests := []struct {
		name string
		err  error
		out  string
		want bool
	}{
		{"typed sentinel, no text", fmt.Errorf("listing: %w", ErrNoServer), "", true},
		{"text fallback, untyped error", errors.New("exit status 1"), "no server running on /tmp/x", true},
		{"text fallback, socket never created", errors.New("exit status 1"), "error connecting to /tmp/x", true},
		{"text fallback, server auto-exited after last session", errors.New("exit status 1"), "server exited unexpectedly", true},
		{"a real failure is not 'no server'", errors.New("exit status 1"), "duplicate session: foo", false},
		{"no error at all", nil, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NoServerRunning(tt.err, tt.out); got != tt.want {
				t.Errorf("NoServerRunning = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestFindSessionIDTreatsNoServerAsEmpty pins the behaviour the sentinel exists
// to protect: nothing running is an ordinary outcome, not an error.
func TestFindSessionIDTreatsNoServerAsEmpty(t *testing.T) {
	r := &recordingRunner{out: "", err: fmt.Errorf("listing: %w", ErrNoServer)}
	id, ok, err := FindSessionID(r, "anything")
	if err != nil {
		t.Fatalf("FindSessionID with no server = %v, want no error", err)
	}
	if ok || id != "" {
		t.Errorf("FindSessionID = (%q, %v), want empty", id, ok)
	}
}
