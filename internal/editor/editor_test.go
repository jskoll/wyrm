package editor

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// stubShell replaces the login-shell probe for the duration of a test.
func stubShell(t *testing.T, value string) {
	t.Helper()
	prev := lookupShellEditor
	lookupShellEditor = func() string { return value }
	t.Cleanup(func() { lookupShellEditor = prev })
}

func TestResolvePrefersEnv(t *testing.T) {
	t.Setenv("EDITOR", "nvim")
	stubShell(t, "emacs")

	got, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"nvim"}) {
		t.Errorf("Resolve() = %q, want [nvim]", got)
	}
}

func TestResolveKeepsEditorArgs(t *testing.T) {
	t.Setenv("EDITOR", "code -w")

	got, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"code", "-w"}) {
		t.Errorf("Resolve() = %q, want [code -w]", got)
	}
}

// The tmux case: the server's environment carries no EDITOR, so without the
// shell probe every popup-launched TUI would edit configs in vi.
func TestResolveFallsBackToShell(t *testing.T) {
	t.Setenv("EDITOR", "")
	stubShell(t, "nvim")

	got, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"nvim"}) {
		t.Errorf("Resolve() = %q, want [nvim] from the shell probe", got)
	}
}

func TestResolveFallsBackToVi(t *testing.T) {
	t.Setenv("EDITOR", "")
	stubShell(t, "")

	got, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{Fallback}) {
		t.Errorf("Resolve() = %q, want [%s]", got, Fallback)
	}
}

func TestResolveRejectsBlankEditor(t *testing.T) {
	t.Setenv("EDITOR", "   ")
	stubShell(t, "nvim")

	if _, err := Resolve(); err == nil {
		t.Fatal("Resolve() = nil error, want an error for a whitespace-only $EDITOR")
	}
}

func TestCommandAppendsPath(t *testing.T) {
	t.Setenv("EDITOR", "code -w")

	cmd, err := Command("/tmp/x.wyrm.toml")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"code", "-w", "/tmp/x.wyrm.toml"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("cmd.Args = %q, want %q", cmd.Args, want)
	}
}

// shellEditor is the one piece that actually starts a process; exercise it
// against a stub shell so the -lic contract doesn't rot unnoticed.
func TestShellEditorReadsShellOutput(t *testing.T) {
	dir := t.TempDir()
	shell := filepath.Join(dir, "fake-shell.sh")
	script := "#!/bin/sh\nprintf 'nvim\\n'\n"
	if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)

	if got := shellEditor(); got != "nvim" {
		t.Errorf("shellEditor() = %q, want %q", got, "nvim")
	}
}

func TestShellEditorToleratesFailure(t *testing.T) {
	dir := t.TempDir()
	shell := filepath.Join(dir, "fake-shell.sh")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)

	if got := shellEditor(); got != "" {
		t.Errorf("shellEditor() = %q, want empty on a failing shell", got)
	}
}

func TestShellEditorWithoutShellSet(t *testing.T) {
	t.Setenv("SHELL", "")

	if got := shellEditor(); got != "" {
		t.Errorf("shellEditor() = %q, want empty when $SHELL is unset", got)
	}
}
