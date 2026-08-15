package main

// clone clones a git repository and starts a session for it in one step —
// wyrm's second-ever dependency on a binary other than tmux (after
// selfupdate's use of the network), scoped to this one explicit subcommand
// rather than any default behavior.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jskoll/wyrm/internal/config"
)

// clone runs `git clone`, then behaves like a bare `wyrm up` run from inside
// the freshly cloned directory: local config, shared config (named for the
// directory), user default, or built-in default — whichever discovery would
// already find. If a [[wildcard]] pattern covers the destination, that
// template is used instead, the same as it would be for any other directory
// under the pattern.
func (a *app) clone(args []string) error {
	fs := a.newFlagSet("clone")
	noStart := fs.Bool("no-start", false, "clone the repository without starting a session")
	fs.BoolVar(noStart, "n", false, "alias for -no-start")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		return usageErrf("wyrm clone needs a repository and an optional destination directory: wyrm clone [-no-start] <repo> [dest]")
	}
	repo, dest := fs.Arg(0), fs.Arg(1)

	if strings.HasPrefix(repo, "-") {
		return usageErrf("invalid repository %q", repo)
	}
	if dest != "" && strings.HasPrefix(dest, "-") {
		return usageErrf("invalid destination directory %q", dest)
	}

	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("wyrm clone requires git on PATH")
	}

	gitArgs := []string{"clone", "--", repo}
	if dest != "" {
		gitArgs = append(gitArgs, dest)
	}
	cmd := exec.Command("git", gitArgs...)
	cmd.Stdout, cmd.Stderr = a.stdout, a.stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone: %w", err)
	}

	if dest == "" {
		dest = deriveCloneDir(repo)
	}
	absDest, err := filepath.Abs(dest)
	if err != nil {
		return err
	}

	if *noStart {
		_, _ = fmt.Fprintf(a.stdout, "cloned %s to %s\n", repo, absDest)
		return nil
	}

	for _, name := range []string{config.DefaultFileName, config.LegacyFileName} {
		cfgPath := filepath.Join(absDest, name)
		if cfg, err := config.Load(cfgPath); err == nil {
			if cfg.Session.OnProjectStart != "" || cfg.Session.OnProjectFirstStart != "" {
				_, _ = fmt.Fprintf(a.stderr, "wyrm: warning: %s defines lifecycle hooks (run with -no-start to clone without executing hooks)\n", name)
			}
			break
		}
	}

	settings, err := config.LoadSettings()
	if err != nil {
		return err
	}
	// A wildcard pattern covering the destination takes priority over
	// whatever discovery would find inside it — matching how any other
	// directory under that pattern behaves, clone included or not.
	if project, found := config.FindProject(settings, filepath.Base(absDest)); found && project.Wildcard && project.Root == absDest {
		return a.startProject(project, nil)
	}

	if err := os.Chdir(absDest); err != nil {
		return fmt.Errorf("entering %s: %w", absDest, err)
	}
	return a.up(nil)
}

// deriveCloneDir mirrors git's own destination-directory derivation when no
// explicit dest is given: the repository's name with a trailing ".git"
// (and any trailing slash) stripped, taking the last path segment so an
// scp-style "host:user/repo.git" address resolves the same way a URL does.
func deriveCloneDir(repo string) string {
	trimmed := strings.TrimSuffix(strings.TrimRight(repo, "/"), ".git")
	return filepath.Base(trimmed)
}
