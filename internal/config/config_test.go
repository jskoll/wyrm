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
		{"bad pane title position", `[session]
name = "x"
pane_title_position = "middle"
[[windows]]
name = "w"`, `pane_title_position must be "top" or "bottom"`},
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
			name, root, err := tt.session.Resolve("")
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
	// t.Chdir restores the previous directory itself, and refuses to run in a
	// parallel test — which is the bug the hand-rolled version could not catch.
	t.Chdir(dir)
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

// TestLoadWarnsOnUnknownKeys: a misspelled key is dropped silently by a plain
// TOML unmarshal, so a config whose every key was a typo passed `wyrm validate`
// — the exact mistake validate exists to catch.
func TestLoadWarnsOnUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultFileName)
	//nolint:misspell // the misspellings are the fixture: this is what the warning must catch.
	body := "[session]\nnmae = \"x\"\nroot = \".\"\n\n[[windows]]\nname = \"w\"\n\n" +
		"  [[windows.splits]]\n  comand = \"nvim\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	joined := strings.Join(cfg.Warnings(), "\n")
	for _, want := range []string{`"session.nmae"`, `"windows.splits.comand"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings = %q, want one naming %s", joined, want)
		}
	}
	// The correctly-spelled keys still have to land.
	if cfg.Session.Root != "." || len(cfg.Windows) != 1 {
		t.Errorf("cfg = %+v, want the valid keys decoded despite the typos", cfg)
	}
}

// TestLoadAcceptsEveryDocumentedKey guards the strict decoder against becoming
// a false alarm: every key the reference documents must round-trip warning-free.
func TestLoadAcceptsEveryDocumentedKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultFileName)
	body := `
[session]
name = "s"
root = "."
on_project_start = "true"
on_project_exit = "true"
on_project_first_start = "true"
on_project_restart = "true"
startup_window = "w"
startup_pane = 0
aliases = ["s2", "s3"]
enable_pane_titles = true
pane_title_position = "top"
pane_title_format = "#{pane_index}"

[[windows]]
name = "w"
layout = "tiled"
pre_window = "true"
post_window = "true"
synchronize = true
synchronize_panes = true
remain_on_exit = true

  [[windows.splits]]
  type = "h"
  size = 30
  command = "nvim"
  remain_on_exit = true
  zoomed = true
  zoom = true

    [[windows.splits.children]]
    type = "v"
    size = 50
    command = "top"

[[windows]]
name = "legacy"

  [[windows.panes]]
  command = "htop"
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, w := range cfg.Warnings() {
		if strings.Contains(w, "unknown key") {
			t.Errorf("documented key reported as unknown: %s", w)
		}
	}
}

func TestInterpolate(t *testing.T) {
	path := writeConfig(t, `
[session]
name = "app-{{.port}}"
root = "~/code/{{branch}}"
on_project_start = "echo start {{port}}"

[session.env]
PORT = "{{.port}}"
BRANCH = "$branch"

[[windows]]
name = "server-{{.port}}"
root = "{{subdir}}"
pre_window = "export P={{port}}"
post_window = "curl http://localhost:{{port}}"

  [[windows.splits]]
  command = "npm run dev -- --port={{port}}"
  root = "api/{{branch}}"

  [[windows.splits]]
  type = "h"
  run = "go run main.go -p {{.port}}"

    [[windows.splits.children]]
    type = "v"
    command = "echo ${port}"

[[windows]]
name = "legacy"
  [[windows.panes]]
  command = "ping localhost:{{port}}"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.Interpolate(map[string]string{
		"port":   "8080",
		"branch": "feat-login",
		"subdir": "frontend",
		"extra":  "value",
	})

	if cfg.Session.Name != "app-8080" {
		t.Errorf("Session.Name = %q, want %q", cfg.Session.Name, "app-8080")
	}
	if cfg.Session.Root != "~/code/feat-login" {
		t.Errorf("Session.Root = %q, want %q", cfg.Session.Root, "~/code/feat-login")
	}
	if cfg.Session.OnProjectStart != "echo start 8080" {
		t.Errorf("Session.OnProjectStart = %q, want %q", cfg.Session.OnProjectStart, "echo start 8080")
	}
	if cfg.Session.Env["PORT"] != "8080" {
		t.Errorf("Env[PORT] = %q, want %q", cfg.Session.Env["PORT"], "8080")
	}
	if cfg.Session.Env["BRANCH"] != "feat-login" {
		t.Errorf("Env[BRANCH] = %q, want %q", cfg.Session.Env["BRANCH"], "feat-login")
	}
	if cfg.Session.Env["extra"] != "value" {
		t.Errorf("Env[extra] = %q, want %q", cfg.Session.Env["extra"], "value")
	}

	if cfg.Windows[0].Name != "server-8080" {
		t.Errorf("Windows[0].Name = %q, want %q", cfg.Windows[0].Name, "server-8080")
	}
	if cfg.Windows[0].Root != "frontend" {
		t.Errorf("Windows[0].Root = %q, want %q", cfg.Windows[0].Root, "frontend")
	}
	if cfg.Windows[0].PreWindow != "export P=8080" {
		t.Errorf("Windows[0].PreWindow = %q, want %q", cfg.Windows[0].PreWindow, "export P=8080")
	}
	if cfg.Windows[0].PostWindow != "curl http://localhost:8080" {
		t.Errorf("Windows[0].PostWindow = %q, want %q", cfg.Windows[0].PostWindow, "curl http://localhost:8080")
	}
	if cfg.Windows[0].Splits[0].Command != "npm run dev -- --port=8080" {
		t.Errorf("Splits[0].Command = %q, want %q", cfg.Windows[0].Splits[0].Command, "npm run dev -- --port=8080")
	}
	if cfg.Windows[0].Splits[0].Root != "api/feat-login" {
		t.Errorf("Splits[0].Root = %q, want %q", cfg.Windows[0].Splits[0].Root, "api/feat-login")
	}
	if cfg.Windows[0].Splits[1].Run != "go run main.go -p 8080" {
		t.Errorf("Splits[1].Run = %q, want %q", cfg.Windows[0].Splits[1].Run, "go run main.go -p 8080")
	}
	if cfg.Windows[0].Splits[1].Children[0].Command != "echo 8080" {
		t.Errorf("Children[0].Command = %q, want %q", cfg.Windows[0].Splits[1].Children[0].Command, "echo 8080")
	}
	if cfg.Windows[1].Panes[0].Command != "ping localhost:8080" {
		t.Errorf("Panes[0].Command = %q, want %q", cfg.Windows[1].Panes[0].Command, "ping localhost:8080")
	}
}
