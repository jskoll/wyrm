package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.wyrm.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadValid(t *testing.T) {
	path := writeConfig(t, `
[session]
name = "myproject"
root = "/tmp/myproject"
startup_window = "editor"
startup_pane = 0

[[windows]]
name = "editor"
pre_window = "nvm use 18"

  [[windows.splits]]
  command = "nvim"

  [[windows.splits]]
  type = "h"
  size = 30
  command = "npm run dev"

    [[windows.splits.children]]
    type = "v"
    command = "# comment"

[[windows]]
name = "tests"
layout = "tiled"

[[windows.panes]]
command = "npm test"

[[windows.panes]]
command = "npm run lint"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Session.Name != "myproject" {
		t.Errorf("Name = %q, want myproject", cfg.Session.Name)
	}
	if cfg.Session.StartupPane == nil || *cfg.Session.StartupPane != 0 {
		t.Errorf("StartupPane = %v, want pointer to 0", cfg.Session.StartupPane)
	}
	if len(cfg.Windows) != 2 {
		t.Fatalf("len(Windows) = %d, want 2", len(cfg.Windows))
	}
	if got := len(cfg.Windows[0].Splits); got != 2 {
		t.Errorf("window 0 splits = %d, want 2", got)
	}
	if got := len(cfg.Windows[0].Splits[1].Children); got != 1 {
		t.Errorf("split 1 children = %d, want 1", got)
	}
	if got := len(cfg.Windows[1].Panes); got != 2 {
		t.Errorf("window 1 panes = %d, want 2", got)
	}
}

func TestLoadStartupPaneUnsetIsNil(t *testing.T) {
	path := writeConfig(t, `
[session]
name = "x"
[[windows]]
name = "w"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Session.StartupPane != nil {
		t.Errorf("StartupPane = %v, want nil when unset", *cfg.Session.StartupPane)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name, content, wantErr string
	}{
		{"invalid toml", `[session`, "parsing"},
		{"missing name and root", `[[windows]]
name = "w"`, "session.name or session.root"},
		{"bad split type", `[session]
name = "x"
[[windows]]
name = "w"
  [[windows.splits]]
  type = "diagonal"`, `unknown type "diagonal"`},
		{"bad split size", `[session]
name = "x"
[[windows]]
name = "w"
  [[windows.splits]]
  type = "h"
  size = 150`, "size must be 1-99"},
		{"bad nested child", `[session]
name = "x"
[[windows]]
name = "w"
  [[windows.splits]]
  type = "h"
    [[windows.splits.children]]
    type = "sideways"`, `unknown type "sideways"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.content))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Load error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	t.Setenv("WYRM_TEST_DIR", "/tmp/envproject")

	tests := []struct {
		name     string
		session  Session
		wantName string
		wantRoot string
	}{
		{"explicit name", Session{Name: "given", Root: "/tmp/foo"}, "given", "/tmp/foo"},
		{"name from root basename", Session{Root: "/tmp/derived"}, "derived", "/tmp/derived"},
		{"env expansion", Session{Root: "$WYRM_TEST_DIR"}, "envproject", "/tmp/envproject"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, root, err := tt.session.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
			if root != tt.wantRoot {
				t.Errorf("root = %q, want %q", root, tt.wantRoot)
			}
		})
	}
}

// chdir switches the working directory for one test (t.Chdir needs go 1.24;
// this module supports 1.21).
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

func TestDiscover(t *testing.T) {
	chdir(t, t.TempDir())

	if _, err := Discover(); err == nil {
		t.Error("Discover in empty dir: want error, got nil")
	}

	if err := os.WriteFile(LegacyFileName, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := Discover(); got != LegacyFileName {
		t.Errorf("Discover = %q, want legacy fallback %q", got, LegacyFileName)
	}

	if err := os.WriteFile(DefaultFileName, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := Discover(); got != DefaultFileName {
		t.Errorf("Discover = %q, want %q preferred", got, DefaultFileName)
	}
}
