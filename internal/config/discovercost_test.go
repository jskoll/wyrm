package config

import (
	"os"
	"path/filepath"
	"testing"
)

// wildcardFixture builds a settings object with one "<base>/*" wildcard and
// returns the base directory the pattern matches in.
func wildcardFixture(t *testing.T) (*Settings, string) {
	t.Helper()
	home := t.TempDir()
	base := filepath.Join(home, "code")
	if err := os.MkdirAll(filepath.Join(base, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	tmpl := filepath.Join(home, "tmpl.wyrm.toml")
	if err := os.WriteFile(tmpl, []byte("[session]\nroot = \".\"\n\n[[windows]]\nname = \"w\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	InvalidateWildcardCache()
	t.Cleanup(InvalidateWildcardCache)
	return &Settings{Wildcard: []Wildcard{{Pattern: filepath.Join(base, "*"), Config: tmpl}}}, base
}

// The TUI re-runs discovery every few seconds, and a "/**" pattern is a full
// recursive walk. Caching is what keeps that off the filesystem; this pins the
// behaviour both ways, since a cache nobody can clear is its own bug.
func TestWildcardMatchesAreCachedAndInvalidatable(t *testing.T) {
	settings, base := wildcardFixture(t)

	if got := len(DiscoverWildcardProjects(settings)); got != 1 {
		t.Fatalf("got %d projects, want 1", got)
	}

	// A directory created after the first walk is not visible yet: that is the
	// cache doing its job, and the cost of not walking the tree every tick.
	if err := os.MkdirAll(filepath.Join(base, "beta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := len(DiscoverWildcardProjects(settings)); got != 1 {
		t.Errorf("got %d projects, want the cached 1", got)
	}

	// Asking explicitly must see it — this is what the TUI's "R" key does.
	InvalidateWildcardCache()
	if got := len(DiscoverWildcardProjects(settings)); got != 2 {
		t.Errorf("after invalidation got %d projects, want 2", got)
	}
}

// ProjectIndex exists so bulk callers stop re-running discovery per session.
// Whatever it does for speed, it has to resolve names exactly as FindProject
// does, including preferring an exact name over another project's alias.
func TestProjectIndexMatchesFindProject(t *testing.T) {
	home := t.TempDir()
	shared := filepath.Join(home, "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(shared, name+DefaultFileName), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("api", "[session]\nname = \"api\"\naliases = [\"web\"]\n\n[[windows]]\nname = \"w\"\n")
	write("web", "[session]\nname = \"web\"\n\n[[windows]]\nname = \"w\"\n")

	settings := &Settings{Storage: StorageShared, SharedDir: shared}
	chdirTest(t, t.TempDir())

	index := NewProjectIndex(settings)
	for _, name := range []string{"api", "web", "nope"} {
		want, wantOK := FindProject(settings, name)
		got, gotOK := index.Find(name)
		if gotOK != wantOK || got.Name != want.Name || got.Path != want.Path {
			t.Errorf("Find(%q) = (%+v, %v), FindProject = (%+v, %v)", name, got, gotOK, want, wantOK)
		}
	}

	// "web" is both a real project and api's alias. The real project wins.
	if p, ok := index.Find("web"); !ok || p.Name != "web" {
		t.Errorf(`Find("web") = (%+v, %v), want the project named web, not api's alias`, p, ok)
	}
}
