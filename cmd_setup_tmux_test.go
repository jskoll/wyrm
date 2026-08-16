package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupTmuxOutput(t *testing.T) {
	t.Run("default output prints snippet", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"setup-tmux"}, &stdout, &stderr, &fakeRunner{}, func() bool { return false }, nil)
		if code != 0 {
			t.Fatalf("code = %d, stderr = %q", code, stderr.String())
		}
		out := stdout.String()
		if !strings.Contains(out, "bind-key C-j display-popup -E -w 80% -h 80% \"wyrm pick\"") {
			t.Errorf("stdout = %q, want pick popup binding", out)
		}
		if !strings.Contains(out, "bind-key C-w display-popup -E -w 90% -h 85% \"wyrm tui\"") {
			t.Errorf("stdout = %q, want tui popup binding", out)
		}
		if !strings.Contains(out, "wyrm status --format tmux") {
			t.Errorf("stdout = %q, want status right binding", out)
		}
	})

	t.Run("custom keys", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"setup-tmux", "-key-pick", "M-j", "-key-tui", "M-w", "-status=false"}, &stdout, &stderr, &fakeRunner{}, func() bool { return false }, nil)
		if code != 0 {
			t.Fatalf("code = %d, stderr = %q", code, stderr.String())
		}
		out := stdout.String()
		if !strings.Contains(out, "bind-key M-j display-popup") {
			t.Errorf("stdout = %q, want M-j binding", out)
		}
		if !strings.Contains(out, "bind-key M-w display-popup") {
			t.Errorf("stdout = %q, want M-w binding", out)
		}
		if strings.Contains(out, "wyrm status") {
			t.Errorf("stdout = %q, want status excluded", out)
		}
	})

	t.Run("append to file with duplicate detection", func(t *testing.T) {
		dir := t.TempDir()
		confFile := filepath.Join(dir, "tmux.conf")
		t.Setenv("TMUX_CONF", confFile)

		// First write
		var stdout, stderr bytes.Buffer
		code := run([]string{"setup-tmux", "-a"}, &stdout, &stderr, &fakeRunner{}, func() bool { return false }, nil)
		if code != 0 {
			t.Fatalf("first run: code = %d, stderr = %q", code, stderr.String())
		}
		content, err := os.ReadFile(confFile)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if !strings.Contains(string(content), "bind-key C-j") {
			t.Errorf("content = %q, want C-j binding", string(content))
		}

		// Second run (duplicate)
		stdout.Reset()
		stderr.Reset()
		code = run([]string{"setup-tmux", "-a"}, &stdout, &stderr, &fakeRunner{}, func() bool { return false }, nil)
		if code != 0 {
			t.Fatalf("second run: code = %d, stderr = %q", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "already present") {
			t.Errorf("stdout = %q, want already present", stdout.String())
		}
	})
}
