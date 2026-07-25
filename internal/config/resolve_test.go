package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveExpandsTilde guards the inconsistency where settings.shared_dir
// expanded "~" but session.root did not, so root = "~/code/app" silently
// produced a literal directory named "~" under the working directory.
func TestResolveExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	_, root, err := Session{Root: "~/code/app"}.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := filepath.Join(home, "code", "app"); root != want {
		t.Errorf("root = %q, want %q", root, want)
	}
	if strings.Contains(root, "~") {
		t.Errorf("root %q still contains a literal ~", root)
	}
}

// TestResolveUndefinedVariableErrors guards against os.ExpandEnv's fail-open
// behavior: "$PROJECTS/api" with PROJECTS unset became "/api", and wyrm
// cheerfully rooted a session there.
func TestResolveUndefinedVariableErrors(t *testing.T) {
	os.Unsetenv("WYRM_TEST_UNSET")
	_, _, err := Session{Root: "$WYRM_TEST_UNSET/api"}.Resolve("")
	if err == nil {
		t.Fatal("Resolve = nil error, want an undefined-variable error")
	}
	if !strings.Contains(err.Error(), "WYRM_TEST_UNSET") {
		t.Errorf("error = %v, want it to name the missing variable", err)
	}
}

func TestResolveExpandsDefinedVariable(t *testing.T) {
	t.Setenv("WYRM_TEST_ROOT", "/tmp/projects")
	_, root, err := Session{Root: "$WYRM_TEST_ROOT/api"}.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if root != filepath.Join("/tmp/projects", "api") {
		t.Errorf("root = %q, want /tmp/projects/api", root)
	}
}

// TestResolveRelativeRootUsesBaseDir is the fix for the TUI starting a project
// in the wrong directory: a relative root has to resolve against the config's
// own location, not against wherever the process happens to be standing.
func TestResolveRelativeRootUsesBaseDir(t *testing.T) {
	base := t.TempDir()
	name, root, err := Session{Root: "."}.Resolve(base)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if root != base {
		t.Errorf("root = %q, want %q", root, base)
	}
	if name != filepath.Base(base) {
		t.Errorf("name = %q, want %q", name, filepath.Base(base))
	}
}

// TestResolveAbsoluteRootIgnoresBaseDir: baseDir only fills in for relative
// roots, so a shared config with an absolute root is unaffected.
func TestResolveAbsoluteRootIgnoresBaseDir(t *testing.T) {
	_, root, err := Session{Root: "/tmp/elsewhere"}.Resolve(t.TempDir())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if root != "/tmp/elsewhere" {
		t.Errorf("root = %q, want /tmp/elsewhere", root)
	}
}

// TestLoadRecordsDir pins the wiring that makes the above reachable: Load has
// to remember where the file came from for Resolve to have a base at all.
func TestLoadRecordsDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultFileName)
	if err := os.WriteFile(path, []byte("[session]\nroot = \".\"\n\n[[windows]]\nname = \"w\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Dir() != dir {
		t.Errorf("Dir() = %q, want %q", cfg.Dir(), dir)
	}
	_, root, err := cfg.Session.Resolve(cfg.Dir())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if root != dir {
		t.Errorf("root = %q, want the config's own directory %q", root, dir)
	}
}

func TestValidateRejectsNoWindows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultFileName)
	if err := os.WriteFile(path, []byte("[session]\nname = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load = nil error, want a no-windows error — session.Create would refuse this config")
	}
}

// TestWarningsFlagLikelyMistakes covers the non-fatal diagnostics: config that
// builds, but not the way its author meant.
func TestWarningsFlagLikelyMistakes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultFileName)
	content := `
[session]
name = "x"

[[windows]]
name = "w"
layout = "tiled"

  [[windows.splits]]
  type = "h"
  command = "nvim"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	joined := strings.Join(cfg.Warnings(), "\n")
	for _, want := range []string{"layout", "first split"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings = %q, want one mentioning %q", joined, want)
		}
	}
}

func TestLegacyFileNameWarns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, LegacyFileName)
	if err := os.WriteFile(path, []byte("[session]\nname = \"x\"\n\n[[windows]]\nname = \"w\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(strings.Join(cfg.Warnings(), "\n"), LegacyFileName) {
		t.Errorf("warnings = %v, want a deprecation notice for %s", cfg.Warnings(), LegacyFileName)
	}
}
