package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// findTmuxConfPath hardcoded ~/.config, the one path in the codebase that
// ignored $XDG_CONFIG_HOME — so anyone who moved their config root had the
// snippet appended to a file tmux never reads, and was told it worked.
func TestSetupTmuxHonoursXDGConfigHome(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TMUX_CONF", "")

	conf := filepath.Join(xdg, "tmux", "tmux.conf")
	if err := os.MkdirAll(filepath.Dir(conf), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conf, []byte("set -g mouse on\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := findTmuxConfPath(); got != conf {
		t.Errorf("findTmuxConfPath() = %q, want %q", got, conf)
	}
}

// TMUX_CONF still wins over everything, and a machine with neither falls back
// to ~/.tmux.conf rather than writing into the cwd.
func TestFindTmuxConfPathPrecedence(t *testing.T) {
	t.Run("TMUX_CONF wins", func(t *testing.T) {
		t.Setenv("TMUX_CONF", "/explicit/tmux.conf")
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		if got := findTmuxConfPath(); got != "/explicit/tmux.conf" {
			t.Errorf("got %q, want the explicit TMUX_CONF", got)
		}
	})

	t.Run("falls back to home", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("TMUX_CONF", "")
		t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // exists but has no tmux/tmux.conf
		t.Setenv("HOME", home)
		if got, want := findTmuxConfPath(), filepath.Join(home, ".tmux.conf"); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

// A bad key spec is written straight into tmux.conf, where bind-key fails at
// load time and takes the rest of the user's file with it. Reject it here,
// where the message can name the flag.
func TestSetupTmuxRejectsBadKeySpecs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"empty", []string{"setup-tmux", "-key-pick", ""}},
		{"embedded space", []string{"setup-tmux", "-key-pick", "C-j x"}},
		{"command separator", []string{"setup-tmux", "-key-tui", "C-w; kill-server"}},
		{"quote", []string{"setup-tmux", "-key-tui", `C-"w`}},
		{"comment", []string{"setup-tmux", "-key-pick", "C-j#"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tc.args, &stdout, &stderr, &fakeRunner{}, func() bool { return false }, nil)
			if code != 2 {
				t.Errorf("exit code = %d, want 2 (usage error); stderr = %q", code, stderr.String())
			}
			if stdout.Len() > 0 {
				t.Errorf("a rejected key should produce no snippet, got:\n%s", stdout.String())
			}
		})
	}
}

func TestSetupTmuxAcceptsRealKeySpecs(t *testing.T) {
	for _, key := range []string{"C-j", "M-x", "C-M-a", "F5", "Up", "PageDown", "a"} {
		var stdout, stderr bytes.Buffer
		code := run([]string{"setup-tmux", "-key-pick", key}, &stdout, &stderr, &fakeRunner{},
			func() bool { return false }, nil)
		if code != 0 {
			t.Errorf("key %q rejected: exit %d, stderr = %q", key, code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "bind-key "+key+" ") {
			t.Errorf("key %q missing from snippet:\n%s", key, stdout.String())
		}
	}
}

// -a appends to a file wyrm does not own. Losing the user's tmux.conf to a bad
// append with no way back is not an acceptable failure mode.
func TestSetupTmuxBacksUpBeforeAppending(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "tmux.conf")
	original := "set -g mouse on\nset -g history-limit 50000\n"
	if err := os.WriteFile(conf, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_CONF", conf)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"setup-tmux", "-a"}, &stdout, &stderr, &fakeRunner{},
		func() bool { return false }, nil); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}

	backup, err := os.ReadFile(conf + ".wyrm-backup")
	if err != nil {
		t.Fatalf("no backup written: %v", err)
	}
	if string(backup) != original {
		t.Errorf("backup = %q, want the file exactly as it was", backup)
	}
	if !strings.Contains(stdout.String(), "backed up") {
		t.Errorf("the backup should be reported, got:\n%s", stdout.String())
	}

	updated, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(updated), original) {
		t.Error("appending must not disturb what was already in the file")
	}
	if !strings.Contains(string(updated), "wyrm tmux integration") {
		t.Error("snippet was not appended")
	}
}

// Nothing to lose, nothing to back up: a file that does not exist yet should
// not leave an empty .wyrm-backup beside it.
func TestSetupTmuxSkipsBackupForANewFile(t *testing.T) {
	conf := filepath.Join(t.TempDir(), "tmux.conf")
	t.Setenv("TMUX_CONF", conf)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"setup-tmux", "-a"}, &stdout, &stderr, &fakeRunner{},
		func() bool { return false }, nil); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if _, err := os.Stat(conf + ".wyrm-backup"); err == nil {
		t.Error("backed up a file that did not exist")
	}
}
