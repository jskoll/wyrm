package config

import (
	"os"
	"path/filepath"
	"testing"
)

// A wildcard project's config is a template shared by every directory the
// pattern matched, so only Project.Root says which directory this one is. Three
// CLI paths and one TUI path used to call Load(p.Path) directly and lose it:
// `wyrm kill <project>` reported the wrong session as not running, and
// `wyrm restart -all` built a spurious session rooted in the template's
// directory.
func TestProjectLoadConfigAppliesWildcardRoot(t *testing.T) {
	dir := t.TempDir()
	tmplDir := filepath.Join(dir, "templates")
	if err := os.MkdirAll(tmplDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tmpl := filepath.Join(tmplDir, "base.wyrm.toml")
	body := "[session]\nroot = \".\"\n\n[[windows]]\nname = \"main\"\n"
	if err := os.WriteFile(tmpl, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	projectDir := filepath.Join(dir, "Code", "alpha")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	p := Project{Name: "alpha", Path: tmpl, Root: projectDir, Wildcard: true}
	cfg, err := p.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	name, root, err := cfg.Session.Resolve(cfg.Dir())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if name != "alpha" {
		t.Errorf("session name = %q, want alpha (the matched directory, not the template's)", name)
	}
	if got, want := mustEval(t, root), mustEval(t, projectDir); got != want {
		t.Errorf("session root = %q, want %q", got, want)
	}
}

// A non-wildcard project keeps whatever its own file says.
func TestProjectLoadConfigLeavesPlainProjectAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultFileName)
	body := "[session]\nname = \"plain\"\nroot = \".\"\n\n[[windows]]\nname = \"main\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// Root is set but Wildcard is not: it must be ignored.
	cfg, err := Project{Name: "plain", Path: path, Root: "/somewhere/else"}.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	name, root, err := cfg.Session.Resolve(cfg.Dir())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if name != "plain" {
		t.Errorf("session name = %q, want plain", name)
	}
	if got, want := mustEval(t, root), mustEval(t, dir); got != want {
		t.Errorf("session root = %q, want %q", got, want)
	}
}

// A project with no config file of its own — the TUI's zoxide directories —
// builds from the default config rooted at its directory, and takes its name
// from that directory rather than from the default config. Loading it by path
// failed outright, so such a project could be started but never stopped.
func TestProjectLoadConfigFilelessProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	projectDir := filepath.Join(home, "somewhere", "beta")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg, err := Project{Name: "beta", Root: projectDir}.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	name, root, err := cfg.Session.Resolve(cfg.Dir())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if name != "beta" {
		t.Errorf("session name = %q, want beta", name)
	}
	if got, want := mustEval(t, root), mustEval(t, projectDir); got != want {
		t.Errorf("session root = %q, want %q", got, want)
	}
}

// A user default config that names a session must not name every fileless
// project after it: the name is what the caller looks the running session up
// by, so they would all collide.
func TestProjectLoadConfigFilelessIgnoresDefaultName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "wyrm"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "[session]\nname = \"my-default\"\nroot = \".\"\n\n[[windows]]\nname = \"main\"\n"
	if err := os.WriteFile(filepath.Join(home, "wyrm", UserDefaultFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	projectDir := filepath.Join(home, "gamma")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg, err := Project{Name: "gamma", Root: projectDir}.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	name, _, err := cfg.Session.Resolve(cfg.Dir())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if name != "gamma" {
		t.Errorf("session name = %q, want gamma (the directory, not the default config's name)", name)
	}
}

func mustEval(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}
