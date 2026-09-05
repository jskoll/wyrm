package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jskoll/wyrm/internal/config"
)

func TestInitTemplateNonInteractive(t *testing.T) {
	templates := []struct {
		flagName string
		wantCmd  string
	}{
		{"node", "npm run dev"},
		{"nodejs", "npm run dev"},
		{"python", "pytest -v --tb=short"},
		{"py", "pytest -v --tb=short"},
		{"go", "go test -v ./..."},
		{"golang", "go test -v ./..."},
		{"rust", "cargo test"},
		{"rs", "cargo test"},
		{"monorepo", "services"},
		{"minimal", "main"},
	}

	for _, tc := range templates {
		t.Run(tc.flagName, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)

			var stdout, stderr bytes.Buffer
			app := &app{
				stdout: &stdout,
				stderr: &stderr,
			}

			if err := app.init([]string{"-template", tc.flagName}); err != nil {
				t.Fatalf("init -template %s failed: %v", tc.flagName, err)
			}

			cfgPath := filepath.Join(dir, config.DefaultFileName)
			cfg, err := config.Load(cfgPath)
			if err != nil {
				t.Fatalf("Load(%s) failed: %v", cfgPath, err)
			}

			if len(cfg.Warnings()) > 0 {
				t.Errorf("unexpected warnings: %v", cfg.Warnings())
			}

			content, err := os.ReadFile(cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(content), tc.wantCmd) {
				t.Errorf("expected config to contain %q, got:\n%s", tc.wantCmd, string(content))
			}

			if !strings.Contains(stdout.String(), "wrote .wyrm.toml") {
				t.Errorf("expected stdout to mention wrote .wyrm.toml, got %q", stdout.String())
			}
		})
	}
}

func TestInitUnknownTemplate(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	app := &app{
		stdout: &stdout,
		stderr: &stderr,
	}

	err := app.init([]string{"-template", "invalid-template"})
	if err == nil {
		t.Fatal("expected error for unknown template, got nil")
	}

	if !strings.Contains(err.Error(), "unknown template") {
		t.Errorf("expected error message to mention unknown template, got %v", err)
	}
}

func TestInitInteractiveDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	// Empty stdin will hit EOF and use all default choices
	app := &app{
		stdin:  strings.NewReader("\n\n\n\n\n\n\n\n"),
		stdout: &stdout,
		stderr: &stderr,
	}

	if err := app.init(nil); err != nil {
		t.Fatalf("interactive init failed: %v", err)
	}

	cfgPath := filepath.Join(dir, config.DefaultFileName)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load(%s) failed: %v", cfgPath, err)
	}

	if len(cfg.Windows) != 1 {
		t.Fatalf("expected 1 window, got %d", len(cfg.Windows))
	}
	if cfg.Windows[0].Name != "main" {
		t.Errorf("expected window name 'main', got %q", cfg.Windows[0].Name)
	}
	if len(cfg.Windows[0].Splits) != 2 {
		t.Fatalf("expected 2 splits for default 2-pane vertical, got %d", len(cfg.Windows[0].Splits))
	}
}

func TestInitInteractiveCustomWizard(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// Prompts in order:
	// 1. Session name: "custom-sess"
	// 2. Root directory: "."
	// 3. Method: "1" (custom layout)
	// 4. Window 1 name: "dev"
	// 5. Window 1 layout: "4" (3-pane editor stack)
	// 6. Main command: "nvim"
	// 7. Top-right command: "npm run watch"
	// 8. Bottom-right command: "npm test"
	// 9. Add another window?: "y"
	// 10. Window 2 name: "db"
	// 11. Window 2 layout: "1" (single pane)
	// 12. Window 2 command: "psql"
	// 13. Add another window?: "n"
	input := strings.Join([]string{
		"custom-sess",
		".",
		"1",
		"dev",
		"4",
		"nvim",
		"npm run watch",
		"npm test",
		"y",
		"db",
		"1",
		"psql",
		"n",
	}, "\n") + "\n"

	var stdout, stderr bytes.Buffer
	app := &app{
		stdin:  strings.NewReader(input),
		stdout: &stdout,
		stderr: &stderr,
	}

	if err := app.init(nil); err != nil {
		t.Fatalf("interactive custom init failed: %v", err)
	}

	cfgPath := filepath.Join(dir, config.DefaultFileName)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load(%s) failed: %v", cfgPath, err)
	}

	if cfg.Session.Name != "custom-sess" {
		t.Errorf("expected session name 'custom-sess', got %q", cfg.Session.Name)
	}
	if len(cfg.Windows) != 2 {
		t.Fatalf("expected 2 windows, got %d", len(cfg.Windows))
	}
	if cfg.Windows[0].Name != "dev" {
		t.Errorf("expected window 0 name 'dev', got %q", cfg.Windows[0].Name)
	}
	if cfg.Windows[1].Name != "db" {
		t.Errorf("expected window 1 name 'db', got %q", cfg.Windows[1].Name)
	}
}

func TestInitInteractiveSelectTemplate(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// Prompts:
	// 1. Session name: "my-go-app"
	// 2. Root directory: "."
	// 3. Method: "4" (Go)
	input := strings.Join([]string{
		"my-go-app",
		".",
		"4",
	}, "\n") + "\n"

	var stdout, stderr bytes.Buffer
	app := &app{
		stdin:  strings.NewReader(input),
		stdout: &stdout,
		stderr: &stderr,
	}

	if err := app.init(nil); err != nil {
		t.Fatalf("interactive template select failed: %v", err)
	}

	cfgPath := filepath.Join(dir, config.DefaultFileName)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load(%s) failed: %v", cfgPath, err)
	}

	if cfg.Session.Name != "my-go-app" {
		t.Errorf("expected session name 'my-go-app', got %q", cfg.Session.Name)
	}
}

func TestInitExistingConfigPromptNo(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	cfgPath := filepath.Join(dir, config.DefaultFileName)
	sentinel := "# Original Config"
	if err := os.WriteFile(cfgPath, []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	app := &app{
		stdin:  strings.NewReader("n\n"),
		stdout: &stdout,
		stderr: &stderr,
	}

	err := app.init([]string{"-template", "go"})
	if err == nil {
		t.Fatal("expected error when user says no to overwrite, got nil")
	}

	content, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != sentinel {
		t.Errorf("expected original content to be preserved, got %q", string(content))
	}
}

func TestInitExistingConfigPromptYes(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	cfgPath := filepath.Join(dir, config.DefaultFileName)
	sentinel := "# Original Config"
	if err := os.WriteFile(cfgPath, []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	app := &app{
		stdin:  strings.NewReader("yes\n"),
		stdout: &stdout,
		stderr: &stderr,
	}

	if err := app.init([]string{"-template", "go"}); err != nil {
		t.Fatalf("init failed after confirming overwrite: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load(%s) failed: %v", cfgPath, err)
	}
	if len(cfg.Windows) == 0 {
		t.Errorf("expected windows in overwritten config")
	}
}

func TestInitExistingConfigForce(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	cfgPath := filepath.Join(dir, config.DefaultFileName)
	sentinel := "# Original Config"
	if err := os.WriteFile(cfgPath, []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	app := &app{
		stdin:  strings.NewReader(""), // No input needed because of --force
		stdout: &stdout,
		stderr: &stderr,
	}

	if err := app.init([]string{"-template", "rust", "-force"}); err != nil {
		t.Fatalf("init with -force failed: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load(%s) failed: %v", cfgPath, err)
	}
	if len(cfg.Windows) == 0 {
		t.Errorf("expected windows in overwritten config")
	}
}

func TestInitExplicitConfigPath(t *testing.T) {
	dir := t.TempDir()
	customPath := filepath.Join(dir, "nested", "custom.wyrm.toml")

	var stdout, stderr bytes.Buffer
	app := &app{
		stdout: &stdout,
		stderr: &stderr,
	}

	if err := app.init([]string{"-config", customPath, "-template", "python"}); err != nil {
		t.Fatalf("init -config failed: %v", err)
	}

	cfg, err := config.Load(customPath)
	if err != nil {
		t.Fatalf("Load(%s) failed: %v", customPath, err)
	}
	if len(cfg.Windows) == 0 {
		t.Errorf("expected windows in custom config")
	}
}

// TestInitSharedStorageCanonicalizesRoot is the regression test for a
// generated config's root = "." breaking the moment it's written into the
// shared config directory instead of the project itself: session.root then
// resolves against the shared directory, not the project init was run in.
func TestInitSharedStorageCanonicalizesRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	projectDir := t.TempDir()
	t.Chdir(projectDir)

	sharedDir := t.TempDir()
	settingsDir := filepath.Join(home, ".config", "wyrm")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsContent := "storage = \"shared\"\nshared_dir = \"" + sharedDir + "\"\n"
	if err := os.WriteFile(filepath.Join(settingsDir, config.SettingsFileName), []byte(settingsContent), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	app := &app{stdout: &stdout, stderr: &stderr}
	if err := app.init([]string{"-template", "minimal"}); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	wantPath := filepath.Join(sharedDir, filepath.Base(projectDir)+config.DefaultFileName)
	cfg, err := config.Load(wantPath)
	if err != nil {
		t.Fatalf("Load(%s): %v", wantPath, err)
	}
	if cfg.Session.Root == "." || cfg.Session.Root == "" {
		t.Fatalf("session.root = %q, want it canonicalized to the project's absolute path", cfg.Session.Root)
	}
	wantRoot, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if gotRoot, err := filepath.EvalSymlinks(cfg.Session.Root); err != nil || gotRoot != wantRoot {
		t.Errorf("session.root = %q, want the project directory %q", cfg.Session.Root, wantRoot)
	}
}

func TestInitExtraArgsRejected(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := &app{
		stdout: &stdout,
		stderr: &stderr,
	}

	err := app.init([]string{"stray-argument"})
	if err == nil {
		t.Fatal("expected error on unexpected argument, got nil")
	}
}
