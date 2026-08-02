package config

import (
	"os"
	"path/filepath"
	"strings"
)

// worktreeSeparator is inserted between a linked worktree's own directory
// name and its main repository's, in mainRepoWorktreeName's result.
const worktreeSeparator = "-"

// mainRepoWorktreeName returns a session name that identifies both which
// repository root is a linked git worktree of, and which worktree it is:
// "<main-repo-name>-<worktree-dir-name>". ok is false for anything that
// isn't a linked worktree — a normal repository's root keeps the plain
// basename Session.Resolve already falls back to.
//
// It reads root's ".git" file directly rather than shelling to git, so this
// stays on wyrm's zero-external-dependency naming path — used by every
// `wyrm up`, not just the explicit, git-requiring `wyrm clone`. A linked
// worktree's ".git" is a one-line "gitdir: <path>/.git/worktrees/<name>"
// pointer (a plain directory, for an ordinary checkout, which os.ReadFile
// simply fails to read as a file — the ordinary "not a worktree" case).
//
// The main repo's own name alone is deliberately not used by itself: every
// worktree of one repository would then resolve to the identical session
// name, and FindSessionID would treat the second `wyrm up` as "already
// running" and hand over the wrong worktree entirely. Combining both keeps
// the result unique per worktree directory while still surfacing which repo
// it belongs to — e.g. "wyrm-feature-x" for a worktree directory named
// "feature-x" off the "wyrm" repo.
func mainRepoWorktreeName(root string) (name string, ok bool) {
	data, err := os.ReadFile(filepath.Join(root, ".git"))
	if err != nil {
		return "", false
	}
	gitdir, found := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !found {
		return "", false
	}
	gitdir = strings.TrimSpace(gitdir)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(root, gitdir)
	}

	// A linked worktree's gitdir is "<main-repo>/.git/worktrees/<name>".
	// Cutting at the "/.git/worktrees/" marker reaches the main checkout in
	// one step, without assuming how many path components it has.
	marker := string(filepath.Separator) + ".git" + string(filepath.Separator) + "worktrees" + string(filepath.Separator)
	idx := strings.Index(gitdir, marker)
	if idx <= 0 {
		return "", false
	}
	mainRepo := gitdir[:idx]

	mainName := filepath.Base(mainRepo)
	worktreeName := filepath.Base(root)
	if mainName == "" || worktreeName == "" || mainName == worktreeName {
		return worktreeName, worktreeName != ""
	}
	return mainName + worktreeSeparator + worktreeName, true
}
