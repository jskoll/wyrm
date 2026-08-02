package config

import (
	"os"
	"path/filepath"
	"testing"
)

// setupWorktree writes a ".git" file at worktreeDir pointing at
// <mainRepo>/.git/worktrees/<name>, mimicking what `git worktree add`
// actually leaves behind, without needing git installed to test against.
func setupWorktree(t *testing.T, mainRepo, worktreeDir, name string) {
	t.Helper()
	gitdir := filepath.Join(mainRepo, ".git", "worktrees", name)
	if err := os.MkdirAll(gitdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "gitdir: " + gitdir + "\n"
	if err := os.WriteFile(filepath.Join(worktreeDir, ".git"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMainRepoWorktreeName(t *testing.T) {
	base := t.TempDir()
	mainRepo := filepath.Join(base, "wyrm")
	worktreeDir := filepath.Join(base, "feature-x")
	setupWorktree(t, mainRepo, worktreeDir, "feature-x")

	name, ok := mainRepoWorktreeName(worktreeDir)
	if !ok {
		t.Fatal("mainRepoWorktreeName: ok = false, want true for a linked worktree")
	}
	if want := "wyrm-feature-x"; name != want {
		t.Errorf("name = %q, want %q", name, want)
	}
}

// TestMainRepoWorktreeNameSameBasename covers the collision the "-" join
// exists to avoid becoming silly: a worktree directory that happens to share
// the main repo's own name shouldn't produce "repo-repo".
func TestMainRepoWorktreeNameSameBasename(t *testing.T) {
	base := t.TempDir()
	mainRepo := filepath.Join(base, "main-checkout", "wyrm")
	worktreeDir := filepath.Join(base, "other-checkout", "wyrm")
	setupWorktree(t, mainRepo, worktreeDir, "wyrm-copy")

	name, ok := mainRepoWorktreeName(worktreeDir)
	if !ok {
		t.Fatal("mainRepoWorktreeName: ok = false, want true")
	}
	if want := "wyrm"; name != want {
		t.Errorf("name = %q, want plain %q, not a doubled-up name", name, want)
	}
}

// TestMainRepoWorktreeNameOrdinaryRepo covers the common case: a normal
// checkout's ".git" is a directory, not a file, so os.ReadFile simply fails
// to read it and this reports "not a worktree" rather than erroring.
func TestMainRepoWorktreeNameOrdinaryRepo(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := mainRepoWorktreeName(root); ok {
		t.Error("mainRepoWorktreeName on an ordinary repo: ok = true, want false")
	}
}

func TestMainRepoWorktreeNameNoGit(t *testing.T) {
	if _, ok := mainRepoWorktreeName(t.TempDir()); ok {
		t.Error("mainRepoWorktreeName on a directory with no .git at all: ok = true, want false")
	}
}

// TestMainRepoWorktreeNameMalformedGitdir covers a ".git" file that doesn't
// look like tmux's own worktree layout — e.g. a submodule's ".git" pointer,
// which has the same "gitdir:" shape but no "/worktrees/" segment.
func TestMainRepoWorktreeNameMalformedGitdir(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: ../.git/modules/sub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := mainRepoWorktreeName(root); ok {
		t.Error("mainRepoWorktreeName on a submodule-style gitdir: ok = true, want false")
	}
}

// TestSessionResolveUsesWorktreeName is the integration point: an unnamed
// session rooted at a linked worktree gets the combined name, not the plain
// directory basename.
func TestSessionResolveUsesWorktreeName(t *testing.T) {
	base := t.TempDir()
	mainRepo := filepath.Join(base, "wyrm")
	worktreeDir := filepath.Join(base, "feature-x")
	setupWorktree(t, mainRepo, worktreeDir, "feature-x")

	name, _, err := Session{Root: worktreeDir}.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := "wyrm-feature-x"; name != want {
		t.Errorf("name = %q, want %q", name, want)
	}
}

// TestSessionResolveExplicitNameWinsOverWorktree: an explicit session.name
// always wins, worktree or not — Resolve only falls back to the derived
// name when Name is empty.
func TestSessionResolveExplicitNameWinsOverWorktree(t *testing.T) {
	base := t.TempDir()
	mainRepo := filepath.Join(base, "wyrm")
	worktreeDir := filepath.Join(base, "feature-x")
	setupWorktree(t, mainRepo, worktreeDir, "feature-x")

	name, _, err := Session{Name: "custom", Root: worktreeDir}.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if name != "custom" {
		t.Errorf("name = %q, want the explicit %q", name, "custom")
	}
}
