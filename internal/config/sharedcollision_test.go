package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeSharedConfig plants a shared config for a project rooted at root.
func writeSharedConfig(t *testing.T, path, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "[session]\nroot = \"" + root + "\"\n\n[[windows]]\nname = \"w\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Two projects with the same folder name both mapped to "<base>.wyrm.toml",
// so the second silently read the first one's config — building a session in
// the wrong root, under the wrong name, with no warning.
func TestSharedConfigPathDisambiguatesOnCollision(t *testing.T) {
	home := t.TempDir()
	shared := filepath.Join(home, "shared")
	settings := &Settings{Storage: StorageShared, SharedDir: shared}

	work := filepath.Join(home, "work", "api")
	personal := filepath.Join(home, "personal", "api")
	for _, d := range []string{work, personal} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// First project takes the plain name.
	first, err := settings.SharedConfigPath(work)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := filepath.Base(first), "api"+DefaultFileName; got != want {
		t.Fatalf("first project got %q, want the plain %q", got, want)
	}
	writeSharedConfig(t, first, work)

	// Second project must not be handed the first one's file.
	second, err := settings.SharedConfigPath(personal)
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatalf("both projects resolved to %s — the second would read the first's config", first)
	}
	if filepath.Dir(second) != shared {
		t.Errorf("disambiguated path left the shared dir: %s", second)
	}

	// And the first project still resolves to the file it already has, so
	// nothing on disk needs migrating.
	again, err := settings.SharedConfigPath(work)
	if err != nil {
		t.Fatal(err)
	}
	if again != first {
		t.Errorf("owner's path moved from %s to %s", first, again)
	}

	// Stable across calls: the same project always gets the same file.
	repeat, err := settings.SharedConfigPath(personal)
	if err != nil {
		t.Fatal(err)
	}
	if repeat != second {
		t.Errorf("disambiguated path is not stable: %s then %s", second, repeat)
	}
}

// The whole scheme rests on being able to tell who owns a file. A config that
// predates this — no root, or a relative one — must read as unknown, because
// every caller treats unknown as "assume it is ours" to stay compatible.
func TestSharedConfigOwner(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}

	abs := filepath.Join(dir, "abs.wyrm.toml")
	writeSharedConfig(t, abs, proj)
	if owner, ok := SharedConfigOwner(abs); !ok || owner != proj {
		t.Errorf("absolute root: got (%q, %v), want (%q, true)", owner, ok, proj)
	}

	rel := filepath.Join(dir, "rel.wyrm.toml")
	writeSharedConfig(t, rel, ".")
	if owner, ok := SharedConfigOwner(rel); ok {
		t.Errorf("a relative root resolves against the shared dir and identifies nothing, got %q", owner)
	}

	none := filepath.Join(dir, "none.wyrm.toml")
	if err := os.WriteFile(none, []byte("[session]\nname = \"x\"\n\n[[windows]]\nname = \"w\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := SharedConfigOwner(none); ok {
		t.Error("a config with no root identifies no project")
	}

	if _, ok := SharedConfigOwner(filepath.Join(dir, "missing.wyrm.toml")); ok {
		t.Error("a missing file owns nothing")
	}
}

// DiscoverGlobal is the read side of the same bug: standing in the second
// project, it returned the first project's config.
func TestDiscoverGlobalDoesNotReturnAnotherProjectsConfig(t *testing.T) {
	home := t.TempDir()
	shared := filepath.Join(home, "shared")
	settings := &Settings{Storage: StorageShared, SharedDir: shared}

	work := filepath.Join(home, "work", "api")
	personal := filepath.Join(home, "personal", "api")
	for _, d := range []string{work, personal} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeSharedConfig(t, filepath.Join(shared, "api"+DefaultFileName), work)

	chdirTest(t, personal)
	got, err := DiscoverGlobal(settings)
	if err == nil && got == filepath.Join(shared, "api"+DefaultFileName) {
		t.Fatalf("standing in %s, DiscoverGlobal returned %s — which belongs to %s", personal, got, work)
	}

	// The owner still finds its own config.
	chdirTest(t, work)
	got, err = DiscoverGlobal(settings)
	if err != nil {
		t.Fatalf("owner cannot find its own shared config: %v", err)
	}
	if got != filepath.Join(shared, "api"+DefaultFileName) {
		t.Errorf("owner got %q, want its own shared config", got)
	}
}

func chdirTest(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}
