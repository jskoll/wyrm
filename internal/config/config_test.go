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

func TestLoadDefault(t *testing.T) {
	cfg, err := LoadDefault()
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	if len(cfg.Windows) == 0 {
		t.Fatal("default config defines no windows")
	}
	if cfg.Session.Root == "" {
		t.Error("default config session.root is empty")
	}
}

func TestLoadDefaultErrors(t *testing.T) {
	orig := defaultConfigData
	t.Cleanup(func() { defaultConfigData = orig })

	t.Run("invalid toml", func(t *testing.T) {
		defaultConfigData = []byte("[session")
		_, err := LoadDefault()
		if err == nil || !strings.Contains(err.Error(), "parsing default config") {
			t.Errorf("LoadDefault error = %v, want containing %q", err, "parsing default config")
		}
	})

	t.Run("fails validation", func(t *testing.T) {
		defaultConfigData = []byte("")
		_, err := LoadDefault()
		if err == nil || !strings.Contains(err.Error(), "default config:") {
			t.Errorf("LoadDefault error = %v, want containing %q", err, "default config:")
		}
	})
}

func TestLoadReadError(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "does-not-exist.toml"))
	if err == nil {
		t.Fatal("Load of missing file: want error, got nil")
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

func TestResolveEffectiveExplicitPath(t *testing.T) {
	path := writeConfig(t, `
[session]
name = "explicit"
root = "."
[[windows]]
name = "w"
`)
	cfg, source, err := ResolveEffective(&Settings{Storage: StorageLocal}, path)
	if err != nil {
		t.Fatalf("ResolveEffective: %v", err)
	}
	if source != path {
		t.Errorf("source = %q, want %q", source, path)
	}
	if cfg.Session.Name != "explicit" {
		t.Errorf("Name = %q, want explicit", cfg.Session.Name)
	}
}

func TestResolveEffectiveExplicitPathMissing(t *testing.T) {
	if _, _, err := ResolveEffective(&Settings{Storage: StorageLocal}, filepath.Join(t.TempDir(), "nope.toml")); err == nil {
		t.Error("ResolveEffective with missing explicit path: want error, got nil")
	}
}

func TestResolveEffectiveDiscoversLocal(t *testing.T) {
	chdir(t, t.TempDir())
	content := "[session]\nname = \"local\"\nroot = \".\"\n[[windows]]\nname = \"w\"\n"
	if err := os.WriteFile(DefaultFileName, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, source, err := ResolveEffective(&Settings{Storage: StorageLocal}, "")
	if err != nil {
		t.Fatalf("ResolveEffective: %v", err)
	}
	if source != DefaultFileName {
		t.Errorf("source = %q, want %q", source, DefaultFileName)
	}
	if cfg.Session.Name != "local" {
		t.Errorf("Name = %q, want local", cfg.Session.Name)
	}
}

func TestResolveEffectiveDiscoversShared(t *testing.T) {
	sharedDir := t.TempDir()
	projectDir := t.TempDir()
	chdir(t, projectDir)

	folderName := filepath.Base(projectDir)
	sharedPath := filepath.Join(sharedDir, folderName+DefaultFileName)
	content := "[session]\nname = \"shared\"\nroot = \".\"\n[[windows]]\nname = \"w\"\n"
	if err := os.WriteFile(sharedPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	settings := &Settings{Storage: StorageShared, SharedDir: sharedDir}
	cfg, source, err := ResolveEffective(settings, "")
	if err != nil {
		t.Fatalf("ResolveEffective: %v", err)
	}
	if source != sharedPath {
		t.Errorf("source = %q, want %q", source, sharedPath)
	}
	if cfg.Session.Name != "shared" {
		t.Errorf("Name = %q, want shared", cfg.Session.Name)
	}
}

func TestResolveEffectiveFallsBackToUserDefault(t *testing.T) {
	chdir(t, t.TempDir())
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	dir := filepath.Join(xdg, "wyrm")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "[session]\nname = \"user-default\"\nroot = \".\"\n[[windows]]\nname = \"w\"\n"
	if err := os.WriteFile(filepath.Join(dir, UserDefaultFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, source, err := ResolveEffective(&Settings{Storage: StorageLocal}, "")
	if err != nil {
		t.Fatalf("ResolveEffective: %v", err)
	}
	wantSource := filepath.Join(dir, UserDefaultFileName)
	if source != wantSource {
		t.Errorf("source = %q, want %q", source, wantSource)
	}
	if cfg.Session.Name != "user-default" {
		t.Errorf("Name = %q, want user-default", cfg.Session.Name)
	}
}

func TestResolveEffectiveFallsBackToBuiltInDefault(t *testing.T) {
	chdir(t, t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfg, source, err := ResolveEffective(&Settings{Storage: StorageLocal}, "")
	if err != nil {
		t.Fatalf("ResolveEffective: %v", err)
	}
	if source != "built-in default" {
		t.Errorf("source = %q, want %q", source, "built-in default")
	}
	if cfg == nil {
		t.Error("cfg = nil, want built-in default config")
	}
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
