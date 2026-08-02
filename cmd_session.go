package main

// Verbs that build, attach to, and destroy sessions: up, restart, kill, and the
// bare-name form that resolves to either a running session or a known project.

import (
	"fmt"
	"io"

	"github.com/jskoll/wyrm/internal/config"
	"github.com/jskoll/wyrm/internal/session"
	"github.com/jskoll/wyrm/internal/sessions"
	"github.com/jskoll/wyrm/internal/tmux"
)

// up builds the current folder's session (or attaches if it's already
// running). This is the default when no subcommand is given.
func (a *app) up(args []string) error {
	fs := a.newFlagSet("up")
	configPath := fs.String("config", "", "path to config file (default: .wyrm.toml, then .tmuxconfig)")
	dryRun := fs.Bool("n", false, "print the tmux commands and hooks that would run, without touching tmux")
	detach := fs.Bool("d", false, "build the session without attaching")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrf("unexpected argument %q (attach by name with `wyrm %s`, not `wyrm up %s`)",
			fs.Arg(0), fs.Arg(0), fs.Arg(0))
	}

	settings, err := config.LoadSettings()
	if err != nil {
		return err
	}

	// ResolveEffective mirrors this exact discovery order — explicit path,
	// discovered local/shared file, user default, built-in default — and
	// session.Create reattaches instead of rebuilding when a session by that
	// name is already running, so unrelated sessions elsewhere don't matter.
	cfg, _, err := a.resolveConfig(settings, *configPath)
	if err != nil {
		return err
	}

	if *dryRun {
		return a.dryRunBuild(cfg)
	}

	name, sessionID, created, err := session.Create(a.runner, cfg, a.stdout, a.stderr)
	if err != nil {
		return err
	}
	a.reportCreated(name, created)
	if *detach {
		_, _ = fmt.Fprintf(a.stdout, "run `wyrm %s` to attach\n", name)
		return nil
	}
	return a.attachOrSwitch(sessionID)
}

// reportCreated prints the one line distinguishing a fresh build from a
// reattach, which up, restart, and startProject all owe the user.
func (a *app) reportCreated(name string, created bool) {
	if created {
		_, _ = fmt.Fprintf(a.stdout, "created session %s\n", name)
		return
	}
	_, _ = fmt.Fprintf(a.stdout, "session %s already running, attaching\n", name)
}

// dryRunBuild prints the tmux commands `wyrm up` would issue, and the lifecycle
// hooks it would run, without doing either. A wyrm config executes arbitrary
// shell by design, so being able to read the plan before running it is worth
// the small amount of machinery — session.Create takes a tmux.Runner, so a
// recording one covers the tmux half, and session.DryRun covers the hooks,
// which never go through the Runner at all.
func (a *app) dryRunBuild(cfg *config.Config) error {
	a.dryRunHeader(
		"dry run: no tmux commands are executed, no lifecycle",
		"hooks are run, and an already-running session is not",
		"consulted.")
	dry := tmux.NewDryRun(a.stdout)
	_, _, _, err := session.Create(dry, cfg, io.Discard, a.stderr, session.DryRun(a.stdout))
	return err
}

// dryRunHeader announces a dry run on stdout, ahead of the transcript.
func (a *app) dryRunHeader(lines ...string) {
	for _, l := range lines {
		_, _ = fmt.Fprintln(a.stdout, "# "+l)
	}
}

// teardownDryRun announces a teardown dry run and returns the option that makes
// session.Kill describe itself instead of acting. kill, kill-by-name, and
// restart all want exactly this pair, and each had its own copy of the header
// text — three chances for the wording to drift.
func (a *app) teardownDryRun() []session.Option {
	a.dryRunHeader(
		"dry run: no tmux commands are executed and no",
		"lifecycle hooks are run.")
	return []session.Option{session.DryRun(a.stdout)}
}

// restart tears the session down (running on_project_exit) and builds it
// again from the current config. Editing a config and wanting the session to
// match it is the single most common thing to do next, and `wyrm kill && wyrm`
// is an awkward way to spell it.
func (a *app) restart(args []string) error {
	fs := a.newFlagSet("restart")
	configPath := fs.String("config", "", "path to config file (default: .wyrm.toml, then .tmuxconfig)")
	dryRun := fs.Bool("n", false, "print the tmux commands and hooks that would run, without touching tmux")
	detach := fs.Bool("d", false, "build the session without attaching")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if err := requireNoArgs(fs); err != nil {
		return err
	}
	settings, err := config.LoadSettings()
	if err != nil {
		return err
	}
	cfg, _, err := a.resolveConfig(settings, *configPath)
	if err != nil {
		return err
	}

	if *dryRun {
		// The teardown half consults the real server (see session.Kill's doc),
		// so a not-running session is reported and only the build is described.
		if _, kerr := session.Kill(a.runner, cfg, a.stderr, a.teardownDryRun()...); kerr != nil {
			_, _ = fmt.Fprintf(a.stderr, "wyrm: nothing to stop (%v)\n", kerr)
		}
		dry := tmux.NewDryRun(a.stdout)
		_, _, _, err := session.Create(dry, cfg, io.Discard, a.stderr, session.DryRun(a.stdout))
		return err
	}

	// A session that isn't running is not an error here: restart means "end up
	// with a freshly built session", and that's satisfiable either way.
	if name, kerr := session.Kill(a.runner, cfg, a.stderr); kerr != nil {
		_, _ = fmt.Fprintf(a.stderr, "wyrm: nothing to stop (%v)\n", kerr)
	} else {
		_, _ = fmt.Fprintf(a.stdout, "killed session %s\n", name)
	}

	name, sessionID, _, err := session.Create(a.runner, cfg, a.stdout, a.stderr)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(a.stdout, "created session %s\n", name)
	if *detach {
		_, _ = fmt.Fprintf(a.stdout, "run `wyrm %s` to attach\n", name)
		return nil
	}
	return a.attachOrSwitch(sessionID)
}

// kill runs the on_project_exit hook and destroys the session. With a
// positional name it targets that session instead of the current folder's,
// mirroring `wyrm <name>` — killing by name was previously only possible from
// the picker or the TUI.
func (a *app) kill(args []string) error {
	fs := a.newFlagSet("kill")
	configPath := fs.String("config", "", "path to config file (default: .wyrm.toml, then .tmuxconfig)")
	dryRun := fs.Bool("n", false, "print the hook and kill that would run, without touching tmux")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return usageErrf("unexpected argument %q (kill takes at most one session name)", fs.Arg(1))
	}

	settings, err := config.LoadSettings()
	if err != nil {
		return err
	}

	// A named target resolves through the project list first, so its
	// on_project_exit hook still runs when wyrm knows the config. Falling back
	// to a plain tmux kill covers sessions wyrm didn't create.
	if target := fs.Arg(0); target != "" {
		if *configPath != "" {
			return usageErrf("-config and a session name are mutually exclusive")
		}
		return a.killByName(settings, target, *dryRun)
	}

	cfg, _, err := a.resolveConfig(settings, *configPath)
	if err != nil {
		return err
	}

	var opts []session.Option
	if *dryRun {
		opts = a.teardownDryRun()
	}
	name, err := session.Kill(a.runner, cfg, a.stderr, opts...)
	if err != nil {
		return err
	}
	if !*dryRun {
		_, _ = fmt.Fprintf(a.stdout, "killed session %s\n", name)
	}
	return nil
}

func (a *app) killByName(settings *config.Settings, target string, dryRun bool) error {
	var opts []session.Option
	if dryRun {
		opts = a.teardownDryRun()
	}

	if project, found := config.FindProject(settings, target); found {
		if cfg, err := config.Load(project.Path); err == nil {
			name, kerr := session.Kill(a.runner, cfg, a.stderr, opts...)
			if kerr != nil {
				return kerr
			}
			if !dryRun {
				_, _ = fmt.Fprintf(a.stdout, "killed session %s\n", name)
			}
			return nil
		}
	}

	id, ok, err := tmux.FindSessionID(a.runner, target)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no running session named %q", target)
	}
	// No config, so no hook to run or describe — just the kill itself.
	if dryRun {
		_, _ = fmt.Fprintf(a.stdout, "tmux kill-session -t %s\n", id)
		return nil
	}
	if err := sessions.Kill(a.runner, id); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(a.stdout, "killed session %s\n", target)
	return nil
}

// attachByName attaches or switches directly to the exact-named running
// session, without the interactive picker. This is what shell completion (see
// completions/) completes a bare positional argument to.
//
// Because run()'s default case can't distinguish a fat-fingered subcommand
// from a genuine session name (see knownSubcommands), a not-found error here
// also hints at the nearest known verb when name looks like a typo of one —
// so `wyrm klil` says more than just "no running session named klil".
func (a *app) attachByName(name string) error {
	id, ok, err := tmux.FindSessionID(a.runner, name)
	if err != nil {
		return err
	}
	if ok {
		return a.attachOrSwitch(id)
	}

	// Nothing running by that name — but wyrm may know a *config* by it. This
	// is what makes shared storage worth using: without it, centralizing every
	// project's config buys nothing, because the only way to start one is still
	// to cd into its folder first.
	settings, err := config.LoadSettings()
	if err != nil {
		return err
	}
	if project, found := config.FindProject(settings, name); found {
		return a.startProject(project)
	}

	_, _ = fmt.Fprintf(a.stderr, "wyrm: no running session or known project named %q\n", name)
	if guess, ok := nearestSubcommand(name); ok {
		_, _ = fmt.Fprintf(a.stderr, "wyrm: did you mean the subcommand %q?\n", guess)
	}
	return silent(1)
}

// startProject builds (or reattaches) the session for a discovered config and
// hands the terminal over.
func (a *app) startProject(project config.Project) error {
	cfg, err := config.Load(project.Path)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(a.stderr, "wyrm: using config %s\n", project.Path)
	a.printWarnings(cfg)
	if msg, bad := config.CheckSharedRoot(project, cfg); bad {
		_, _ = fmt.Fprintln(a.stderr, "wyrm: warning: "+msg)
	}

	name, sessionID, created, err := session.Create(a.runner, cfg, a.stdout, a.stderr)
	if err != nil {
		return err
	}
	a.reportCreated(name, created)
	return a.attachOrSwitch(sessionID)
}
