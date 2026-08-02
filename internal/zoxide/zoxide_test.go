package zoxide

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAvailableFalseWhenNotOnPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if Available() {
		t.Error("Available() = true with an empty PATH, want false")
	}
}

func TestAvailableTrueWhenOnPath(t *testing.T) {
	installFakeZoxide(t, "")
	if !Available() {
		t.Error("Available() = false with zoxide on PATH, want true")
	}
}

func TestQueryParsesScoredOutput(t *testing.T) {
	installFakeZoxide(t, "  12.5 /home/user/project-a\n   3.0 /home/user/project-b\n")

	entries, err := Query(0)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	want := []Entry{
		{Path: "/home/user/project-a", Score: 12.5},
		{Path: "/home/user/project-b", Score: 3.0},
	}
	if len(entries) != len(want) {
		t.Fatalf("entries = %+v, want %+v", entries, want)
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Errorf("entries[%d] = %+v, want %+v", i, entries[i], want[i])
		}
	}
}

func TestQueryRespectsLimit(t *testing.T) {
	installFakeZoxide(t, "3.0 /a\n2.0 /b\n1.0 /c\n")

	entries, err := Query(2)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want 2 (limit)", entries)
	}
}

func TestQuerySkipsMalformedLines(t *testing.T) {
	installFakeZoxide(t, "not-a-score /a\n\n5.0 /b\n")

	entries, err := Query(0)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(entries) != 1 || entries[0].Path != "/b" {
		t.Errorf("entries = %+v, want exactly [{Path: /b Score: 5}]", entries)
	}
}

func TestQueryErrorWhenNotInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := Query(0); err == nil {
		t.Error("Query with no zoxide on PATH: want error, got nil")
	}
}

func TestAddInvokesZoxide(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("ZOXIDE_FAKE_ARGS_FILE", argsFile)
	installFakeZoxide(t, "")

	if err := Add("/some/path"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if want := "add /some/path"; string(got) != want+"\n" {
		t.Errorf("Add invoked zoxide with %q, want %q", string(got), want)
	}
}

// installFakeZoxide puts a fake "zoxide" script at the front of PATH. Given
// "query --list --score", it prints queryOutput verbatim; given "add
// <path>", it appends its argv to $ZOXIDE_FAKE_ARGS_FILE if set.
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
