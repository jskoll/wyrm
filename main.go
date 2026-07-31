// Command wyrm creates repeatable tmux session layouts from a TOML config.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/jskoll/wyrm/internal/config"
	"github.com/jskoll/wyrm/internal/editor"
	"github.com/jskoll/wyrm/internal/freeze"
	"github.com/jskoll/wyrm/internal/picker"
	"github.com/jskoll/wyrm/internal/session"
	"github.com/jskoll/wyrm/internal/tmux"
	"github.com/jskoll/wyrm/internal/tui"
	"github.com/pelletier/go-toml/v2"
)

// version is stamped at release time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, tmux.Exec{}, tmux.InsideTmux, tmux.Attach))
}

// run implements the CLI. It takes its dependencies as parameters (rather
// than reaching for globals like os.Stdout or the default flag.CommandLine)
// so tests can drive it without touching real stdio or a real tmux server.
//
// The command surface is git-style subcommands: bare `wyrm` (or `wyrm up`)
// builds or attaches the current folder's session, `wyrm <name>` attaches to
// a running session by name, and every other mode is its own verb (kill,
// pick, tui, save, edit, validate, list, ...). Because each verb is a
// separate branch that parses only its own flags, the old problem of
// silently-ignored or mutually-exclusive top-level flags can't arise.
func run(args []string, stdout, stderr io.Writer, runner tmux.Runner, insideTmux func() bool, attach func(string) error) int {
	if len(args) == 0 {
		return runUp(nil, stdout, stderr, runner, insideTmux, attach)
	}

	switch cmd := args[0]; cmd {
	case "version", "--version", "-version", "-v":
		_, _ = fmt.Fprintln(stdout, "wyrm "+versionString())
		return 0
	case "help", "--help", "-help", "-h":
		printUsage(stdout)
		return 0
	case "up":
		return runUp(args[1:], stdout, stderr, runner, insideTmux, attach)
	case "restart":
		return runRestart(args[1:], stdout, stderr, runner, insideTmux, attach)
	case "kill":
		return runKill(args[1:], stdout, stderr, runner)
	case "pick":
		return runPick(args[1:], stderr, runner, insideTmux, attach)
	case "tui":
		return runTUI(args[1:], stderr, runner, insideTmux, attach)
	case "save":
		return runSave(args[1:], stdout, stderr, runner, insideTmux)
	case "edit":
		return runEdit(args[1:], stderr)
	case "validate":
		return runValidate(args[1:], stdout, stderr)
	case "list":
		return runList(args[1:], stdout, stderr, runner)
	case "list-configs":
		return runListConfigs(args[1:], stdout, stderr)
	case "migrate-config":
		return runMigrateConfig(args[1:], stdout, stderr)
	default:
		if strings.HasPrefix(cmd, "-") {
			// A bare flag with no subcommand (e.g. `wyrm -config x`) drives the
			// default build/attach, so the common `wyrm -config foo` still works.
			return runUp(args, stdout, stderr, runner, insideTmux, attach)
		}
		// Anything else is a running session name to attach to, or a known
		// project to start. This is what shell completion (see completions/)
		// completes a bare argument to.
		return runAttachByName(runner, stdout, stderr, insideTmux, attach, cmd)
	}
}

// versionString returns the release version stamped at build time, or — for an
// unstamped `go install`/`go build` where version is still "dev" — the VCS
// revision the Go toolchain records in the build info, so bug reports carry
// something more useful than a bare "dev". The unstamped form uses semver's
// "+build-metadata" separator (dev+<rev>) rather than a parenthetical, so
// tooling that greps for a semver-ish token isn't thrown by stray parens.
func versionString() string {
	return computeVersionString(version, debug.ReadBuildInfo)
}

// computeVersionString is versionString's logic with debug.ReadBuildInfo
// passed in, so tests can supply a fake *debug.BuildInfo — a real `go test`
// binary doesn't carry VCS settings the way `go build`/`go install` do, so
// the build-info branch below isn't otherwise reachable from a test.
func computeVersionString(version string, readBuildInfo func() (*debug.BuildInfo, bool)) string {
	if version != "dev" {
		return version
	}
	info, ok := readBuildInfo()
	if !ok {
		return version
	}
	var rev, modified string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	if rev == "" {
		return version
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if modified == "true" {
		rev += "-dirty"
	}
	return "dev+" + rev
}

const usage = `wyrm — repeatable tmux session layouts from a TOML config.

Usage:
  wyrm [-config PATH]        build or attach the current folder's session (default)
  wyrm up [-config PATH]     same as bare wyrm, spelled explicitly (-n to dry-run)
  wyrm <name>                attach to a running session, or start a known project, by name
  wyrm restart [-config P]   stop the session and build it again (-n to dry-run)
  wyrm kill [name]           destroy the session (runs on_project_exit first; -n to dry-run)
  wyrm pick                  fuzzy-pick a running session and attach to it
  wyrm tui                   full-screen session manager (browse, preview, manage)
  wyrm save [-config PATH]   save the running session's layout as this folder's config
  wyrm edit [-config PATH]   open the resolved config in $EDITOR, creating one if needed
  wyrm validate [-config P]  check the effective config parses and validates
  wyrm list [-format FMT]    list running sessions (FMT: table, json, toml, names)
  wyrm list-configs          list candidate config file paths (used by shell completion)
  wyrm migrate-config        move the local config into the shared config directory
  wyrm version               print version and exit
  wyrm help                  show this help

Run a subcommand with -h for its own flags.
`

func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, usage)
}

// newFlagSet builds a subcommand flag set that reports parse errors to stderr
// and returns them (ContinueOnError) rather than calling os.Exit, so run stays
// testable. Its Usage prints the subcommand's one-line synopsis (reusing the
// same top-level usage string printUsage does, so there's nothing new to keep
// in sync) ahead of the flag list, rather than a bare stdlib flag dump with no
// context.
func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("wyrm "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		_, _ = fmt.Fprintln(stderr, usageSynopsis(name))
		hasFlags := false
		fs.VisitAll(func(*flag.Flag) { hasFlags = true })
		if hasFlags {
			_, _ = fmt.Fprintln(stderr, "\nFlags:")
			fs.PrintDefaults()
		}
	}
	return fs
}

// usageSynopsis returns the one-line description of a subcommand from the
// top-level usage string (e.g. "wyrm kill [-config PATH]   destroy the
// session (runs on_project_exit first)"), falling back to a bare "wyrm
// <name>" if the string doesn't mention it (which TestSubcommandsListedInUsage
// guards against).
func usageSynopsis(name string) string {
	re := regexp.MustCompile(`(?m)^  (wyrm ` + regexp.QuoteMeta(name) + `\b.*)$`)
	if m := re.FindStringSubmatch(usage); m != nil {
		return strings.TrimRight(m[1], " ")
	}
	return "wyrm " + name
}

// parseFlags parses a subcommand's flags, mapping the outcome to an exit code:
// ok is false when the caller should return the given code — 0 for -h/-help
// (flag prints the usage itself), 2 for a genuine parse error.
func parseFlags(fs *flag.FlagSet, args []string) (code int, ok bool) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, false
		}
		return 2, false
	}
	return 0, true
}

// loadSettings loads the global settings, printing a wyrm-prefixed error and
// signaling failure (ok=false, exit 1) on error.
func loadSettings(stderr io.Writer) (*config.Settings, int, bool) {
	settings, err := config.LoadSettings()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return nil, 1, false
	}
	return settings, 0, true
}

// requireNoArgs rejects leftover positional arguments after a subcommand's
// flags. Without it a mistake like `wyrm list json` (meaning `-format json`)
// is silently ignored and prints the default format instead.
func requireNoArgs(fs *flag.FlagSet, stderr io.Writer) (int, bool) {
	if fs.NArg() == 0 {
		return 0, true
	}
	_, _ = fmt.Fprintf(stderr, "wyrm: unexpected argument %q for %s\n", fs.Arg(0), fs.Name())
	fs.Usage()
	return 2, false
}

// printWarnings reports a config's non-fatal problems (see
// config.Config.Warnings) on stderr, so they're visible without being mixed
// into output a script might be parsing.
func printWarnings(stderr io.Writer, cfg *config.Config) {
	for _, w := range cfg.Warnings() {
		_, _ = fmt.Fprintln(stderr, "wyrm: warning: "+w)
	}
}

// resolveConfig loads the config wyrm would use and reports where it came
// from. With five discovery layers (explicit path, local file, shared file,
// user default, built-in), a user who gets an unexpected session otherwise has
// no way to find out which one was picked — so say so, on stderr, always.
func resolveConfig(settings *config.Settings, explicitPath string, stderr io.Writer) (*config.Config, string, bool) {
	cfg, source, err := config.ResolveEffective(settings, explicitPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return nil, "", false
	}
	_, _ = fmt.Fprintf(stderr, "wyrm: using config %s\n", source)
	printWarnings(stderr, cfg)
	return cfg, source, true
}

// runUp builds the current folder's session (or attaches if it's already
// running). This is the default when no subcommand is given.
func runUp(args []string, stdout, stderr io.Writer, runner tmux.Runner, insideTmux func() bool, attach func(string) error) int {
	fs := newFlagSet("up", stderr)
	configPath := fs.String("config", "", "path to config file (default: .wyrm.toml, then .tmuxconfig)")
	dryRun := fs.Bool("n", false, "print the tmux commands that would run, without touching tmux")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "wyrm: unexpected argument %q (attach by name with `wyrm %s`, not `wyrm up %s`)\n", fs.Arg(0), fs.Arg(0), fs.Arg(0))
		return 2
	}

	settings, code, ok := loadSettings(stderr)
	if !ok {
		return code
	}

	// ResolveEffective mirrors this exact discovery order — explicit path,
	// discovered local/shared file, user default, built-in default — and
	// session.Create reattaches instead of rebuilding when a session by that
	// name is already running, so unrelated sessions elsewhere don't matter.
	cfg, _, ok := resolveConfig(settings, *configPath, stderr)
	if !ok {
		return 1
	}

	if *dryRun {
		return dryRunBuild(cfg, stdout, stderr)
	}

	name, sessionID, created, err := session.Create(runner, cfg, stdout, stderr)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	if created {
		_, _ = fmt.Fprintf(stdout, "created session %s\n", name)
	} else {
		_, _ = fmt.Fprintf(stdout, "session %s already running, attaching\n", name)
	}
	return attachOrSwitch(runner, stderr, insideTmux, attach, sessionID)
}

// dryRunBuild prints the tmux commands `wyrm up` would issue, and the lifecycle
// hooks it would run, without doing either. A wyrm config executes arbitrary
// shell by design, so being able to read the plan before running it is worth
// the small amount of machinery — session.Create takes a tmux.Runner, so a
// recording one covers the tmux half, and session.DryRun covers the hooks,
// which never go through the Runner at all.
func dryRunBuild(cfg *config.Config, stdout, stderr io.Writer) int {
	dryRunHeader(stdout,
		"dry run: no tmux commands are executed, no lifecycle",
		"hooks are run, and an already-running session is not",
		"consulted.")
	dry := tmux.NewDryRun(stdout)
	if _, _, _, err := session.Create(dry, cfg, io.Discard, stderr, session.DryRun(stdout)); err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	return 0
}

// dryRunHeader announces a dry run on stdout, ahead of the transcript.
func dryRunHeader(stdout io.Writer, lines ...string) {
	for _, l := range lines {
		_, _ = fmt.Fprintln(stdout, "# "+l)
	}
}

// runRestart tears the session down (running on_project_exit) and builds it
// again from the current config. Editing a config and wanting the session to
// match it is the single most common thing to do next, and `wyrm kill && wyrm`
// is an awkward way to spell it.
func runRestart(args []string, stdout, stderr io.Writer, runner tmux.Runner, insideTmux func() bool, attach func(string) error) int {
	fs := newFlagSet("restart", stderr)
	configPath := fs.String("config", "", "path to config file (default: .wyrm.toml, then .tmuxconfig)")
	dryRun := fs.Bool("n", false, "print the tmux commands and hooks that would run, without touching tmux")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	if code, ok := requireNoArgs(fs, stderr); !ok {
		return code
	}
	settings, code, ok := loadSettings(stderr)
	if !ok {
		return code
	}
	cfg, _, ok := resolveConfig(settings, *configPath, stderr)
	if !ok {
		return 1
	}

	if *dryRun {
		// The teardown half consults the real server (see session.Kill's doc),
		// so a not-running session is reported and only the build is described.
		dryRunHeader(stdout, "dry run: no tmux commands are executed and no",
			"lifecycle hooks are run.")
		dry := tmux.NewDryRun(stdout)
		if _, err := session.Kill(runner, cfg, stderr, session.DryRun(stdout)); err != nil {
			_, _ = fmt.Fprintf(stderr, "wyrm: nothing to stop (%v)\n", err)
		}
		if _, _, _, err := session.Create(dry, cfg, io.Discard, stderr, session.DryRun(stdout)); err != nil {
			_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
			return 1
		}
		return 0
	}

	// A session that isn't running is not an error here: restart means "end up
	// with a freshly built session", and that's satisfiable either way.
	if name, err := session.Kill(runner, cfg, stderr); err != nil {
		_, _ = fmt.Fprintf(stderr, "wyrm: nothing to stop (%v)\n", err)
	} else {
		_, _ = fmt.Fprintf(stdout, "killed session %s\n", name)
	}

	name, sessionID, _, err := session.Create(runner, cfg, stdout, stderr)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "created session %s\n", name)
	return attachOrSwitch(runner, stderr, insideTmux, attach, sessionID)
}

// runKill runs the on_project_exit hook and destroys the session. With a
// positional name it targets that session instead of the current folder's,
// mirroring `wyrm <name>` — killing by name was previously only possible from
// the picker or the TUI.
func runKill(args []string, stdout, stderr io.Writer, runner tmux.Runner) int {
	fs := newFlagSet("kill", stderr)
	configPath := fs.String("config", "", "path to config file (default: .wyrm.toml, then .tmuxconfig)")
	dryRun := fs.Bool("n", false, "print the hook and kill that would run, without touching tmux")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	if fs.NArg() > 1 {
		_, _ = fmt.Fprintf(stderr, "wyrm: unexpected argument %q (kill takes at most one session name)\n", fs.Arg(1))
		return 2
	}

	settings, code, ok := loadSettings(stderr)
	if !ok {
		return code
	}

	// A named target resolves through the project list first, so its
	// on_project_exit hook still runs when wyrm knows the config. Falling back
	// to a plain tmux kill covers sessions wyrm didn't create.
	if target := fs.Arg(0); target != "" {
		if *configPath != "" {
			_, _ = fmt.Fprintln(stderr, "wyrm: -config and a session name are mutually exclusive")
			return 2
		}
		return killByName(runner, stdout, stderr, settings, target, *dryRun)
	}

	cfg, _, ok := resolveConfig(settings, *configPath, stderr)
	if !ok {
		return 1
	}

	if *dryRun {
		dryRunHeader(stdout, "dry run: no tmux commands are executed and no",
			"lifecycle hooks are run.")
		if _, err := session.Kill(runner, cfg, stderr, session.DryRun(stdout)); err != nil {
			_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
			return 1
		}
		return 0
	}

	name, err := session.Kill(runner, cfg, stderr)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "killed session %s\n", name)
	return 0
}

func killByName(runner tmux.Runner, stdout, stderr io.Writer, settings *config.Settings, target string, dryRun bool) int {
	var opts []session.Option
	if dryRun {
		dryRunHeader(stdout, "dry run: no tmux commands are executed and no",
			"lifecycle hooks are run.")
		opts = append(opts, session.DryRun(stdout))
	}

	if project, found := config.FindProject(settings, target); found {
		if cfg, err := config.Load(project.Path); err == nil {
			name, kerr := session.Kill(runner, cfg, stderr, opts...)
			if kerr != nil {
				_, _ = fmt.Fprintln(stderr, "wyrm: "+kerr.Error())
				return 1
			}
			if dryRun {
				return 0
			}
			_, _ = fmt.Fprintf(stdout, "killed session %s\n", name)
			return 0
		}
	}

	id, ok, err := tmux.FindSessionID(runner, target)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	if !ok {
		_, _ = fmt.Fprintf(stderr, "wyrm: no running session named %q\n", target)
		return 1
	}
	// No config, so no hook to run or describe — just the kill itself.
	if dryRun {
		_, _ = fmt.Fprintf(stdout, "tmux kill-session -t %s\n", id)
		return 0
	}
	if err := picker.KillSession(runner, id); err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "killed session %s\n", target)
	return 0
}

// runAttachByName attaches or switches directly to the exact-named running
// session, without the interactive picker. This is what shell completion (see
// completions/) completes a bare positional argument to.
//
// Because run()'s default case can't distinguish a fat-fingered subcommand
// from a genuine session name (see knownSubcommands), a not-found error here
// also hints at the nearest known verb when name looks like a typo of one —
// so `wyrm klil` says more than just "no running session named klil".
func runAttachByName(runner tmux.Runner, stdout, stderr io.Writer, insideTmux func() bool, attach func(string) error, name string) int {
	id, ok, err := tmux.FindSessionID(runner, name)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	if ok {
		return attachOrSwitch(runner, stderr, insideTmux, attach, id)
	}

	// Nothing running by that name — but wyrm may know a *config* by it. This
	// is what makes shared storage worth using: without it, centralizing every
	// project's config buys nothing, because the only way to start one is still
	// to cd into its folder first.
	settings, code, sok := loadSettings(stderr)
	if !sok {
		return code
	}
	if project, found := config.FindProject(settings, name); found {
		return startProject(runner, stdout, stderr, insideTmux, attach, project)
	}

	_, _ = fmt.Fprintf(stderr, "wyrm: no running session or known project named %q\n", name)
	if guess, ok := nearestSubcommand(name); ok {
		_, _ = fmt.Fprintf(stderr, "wyrm: did you mean the subcommand %q?\n", guess)
	}
	return 1
}

// startProject builds (or reattaches) the session for a discovered config and
// hands the terminal over.
func startProject(runner tmux.Runner, stdout, stderr io.Writer, insideTmux func() bool, attach func(string) error, project config.Project) int {
	cfg, err := config.Load(project.Path)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	_, _ = fmt.Fprintf(stderr, "wyrm: using config %s\n", project.Path)
	printWarnings(stderr, cfg)
	if msg, bad := config.CheckSharedRoot(project); bad {
		_, _ = fmt.Fprintln(stderr, "wyrm: warning: "+msg)
	}

	name, sessionID, created, err := session.Create(runner, cfg, stdout, stderr)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	if created {
		_, _ = fmt.Fprintf(stdout, "created session %s\n", name)
	} else {
		_, _ = fmt.Fprintf(stdout, "session %s already running, attaching\n", name)
	}
	return attachOrSwitch(runner, stderr, insideTmux, attach, sessionID)
}

// knownSubcommands lists the verbs run()'s switch dispatches on (mirrored by
// TestKnownSubcommandsMatchDispatch, which parses the switch itself so this
// list can't silently drift from the real dispatch table). Used only to
// power the "did you mean" hint above.
var knownSubcommands = []string{
	"up", "restart", "kill", "pick", "tui", "save", "edit", "validate",
	"list", "list-configs", "migrate-config", "version", "help",
}

// nearestSubcommand returns the known subcommand closest to name by edit
// distance, when it's close enough to plausibly be a typo (distance <= 2,
// and not so close to name's own length that nearly anything would "match").
func nearestSubcommand(name string) (string, bool) {
	best := ""
	bestDist := -1
	for _, verb := range knownSubcommands {
		d := levenshtein(name, verb)
		if bestDist == -1 || d < bestDist {
			best, bestDist = verb, d
		}
	}
	if bestDist >= 0 && bestDist <= 2 && bestDist < len(best) {
		return best, true
	}
	return "", false
}

// levenshtein returns the edit distance between a and b.
func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := cur[j-1] + 1
			sub := prev[j-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			cur[j] = m
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// runListConfigs prints config paths wyrm knows about: the local file (if
// present) and every candidate in the shared config directory. These are
// the candidates shell completion offers for -config; -config itself can
// point at any of them regardless of the current storage setting.
func runListConfigs(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("list-configs", stderr)
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	if code, ok := requireNoArgs(fs, stderr); !ok {
		return code
	}
	settings, code, ok := loadSettings(stderr)
	if !ok {
		return code
	}
	for _, name := range []string{config.DefaultFileName, config.LegacyFileName} {
		if _, err := os.Stat(name); err == nil {
			_, _ = fmt.Fprintln(stdout, name)
		}
	}
	if dir, err := settings.ResolvedSharedDir(); err == nil {
		matches, _ := filepath.Glob(filepath.Join(dir, "*"+config.DefaultFileName))
		for _, m := range matches {
			_, _ = fmt.Fprintln(stdout, m)
		}
	}
	return 0
}

// runMigrateConfig moves the current directory's local config file into the
// shared config directory, named "<folderName>.wyrm.toml". It does not
// touch the storage setting itself; run this after (or before) switching
// settings.Storage to "shared".
func runMigrateConfig(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("migrate-config", stderr)
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	if code, ok := requireNoArgs(fs, stderr); !ok {
		return code
	}
	settings, code, ok := loadSettings(stderr)
	if !ok {
		return code
	}

	src, err := config.Discover()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: no local config to migrate: "+err.Error())
		return 1
	}

	cwd, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	dst, err := settings.SharedConfigPath(cwd)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}

	if _, err := os.Stat(dst); err == nil {
		_, _ = fmt.Fprintf(stderr, "wyrm: %s already exists, remove it first\n", dst)
		return 1
	} else if !errors.Is(err, os.ErrNotExist) {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	if err := os.Rename(src, dst); err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "moved %s to %s\n", src, dst)
	if settings.Storage != config.StorageShared {
		settingsPath, err := config.SettingsPath()
		if err == nil {
			_, _ = fmt.Fprintf(stdout, "note: set storage = \"shared\" in %s for wyrm to use it\n", settingsPath)
		}
	}
	return 0
}

// runValidate checks that the effective config (the one wyrm would actually
// use) parses and validates, without building a session.
func runValidate(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("validate", stderr)
	configPath := fs.String("config", "", "path to config file (default: .wyrm.toml, then .tmuxconfig)")
	strict := fs.Bool("strict", false, "exit non-zero if the config has warnings (typos, deprecations)")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	if code, ok := requireNoArgs(fs, stderr); !ok {
		return code
	}
	settings, code, ok := loadSettings(stderr)
	if !ok {
		return code
	}
	cfg, source, err := config.ResolveEffective(settings, *configPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	printWarnings(stderr, cfg)
	// Warnings are not failures by default — a deprecated `panes` list still
	// builds the session its author wanted. -strict is for CI, where "this
	// config has a typo in it" should stop the build.
	if *strict && len(cfg.Warnings()) > 0 {
		_, _ = fmt.Fprintf(stderr, "wyrm: %s has %d warning(s) and -strict was given\n", source, len(cfg.Warnings()))
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "config valid: %s\n", source)
	return 0
}

// runEdit opens the resolved config in $EDITOR (falling back to vi), creating
// one at the location wyrm would look next time if none exists yet. After
// the editor exits, a saved-but-invalid file gets a warning rather than an
// error, matching the project's warn-don't-abort philosophy for anything
// that isn't a structural failure.
func runEdit(args []string, stderr io.Writer) int {
	fs := newFlagSet("edit", stderr)
	explicitPath := fs.String("config", "", "path to config file (default: the resolved config)")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	settings, code, ok := loadSettings(stderr)
	if !ok {
		return code
	}

	path := *explicitPath
	if path == "" {
		resolved, _, err := config.EditTarget(settings)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
			return 1
		}
		path = resolved
	}
	// Create the parent directory whichever way the path was arrived at:
	// `wyrm edit -config new/dir/x.toml` used to hand the editor a path it
	// couldn't write, while the flagless form worked.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}

	cmd, err := editor.Command(path)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	runErr := cmd.Run()

	if _, statErr := os.Stat(path); statErr == nil {
		if _, loadErr := config.Load(path); loadErr != nil {
			_, _ = fmt.Fprintf(stderr, "wyrm: warning: %s: %v\n", path, loadErr)
		}
	}

	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			// ExitCode is -1 when the editor was killed by a signal, and
			// os.Exit(-1) reaches the shell as 255. Report the conventional
			// 128+signal instead.
			if code := exitErr.ExitCode(); code >= 0 {
				return code
			}
			return 1
		}
		_, _ = fmt.Fprintln(stderr, "wyrm: "+runErr.Error())
		return 1
	}
	return 0
}

// runSave snapshots a running session's windows, split layout, and foreground
// pane commands into a new config for the current folder (see internal/freeze).
// The target session is the one wyrm is currently attached to when run from
// inside tmux, or the folder's own session (looked up the same way a bare
// `wyrm` would resolve its name) otherwise. Like migrate-config, it refuses to
// overwrite an existing config rather than silently discarding hand-written
// hooks or comments.
func runSave(args []string, stdout, stderr io.Writer, runner tmux.Runner, insideTmux func() bool) int {
	fs := newFlagSet("save", stderr)
	configPath := fs.String("config", "", "path to write the saved config (default: the discovered/shared location)")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	settings, code, ok := loadSettings(stderr)
	if !ok {
		return code
	}

	var sessionID, sessionName string
	if insideTmux() {
		id, name, err := tmux.CurrentSession(runner)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
			return 1
		}
		sessionID, sessionName = id, name
	} else {
		cfg, _, err := config.ResolveEffective(settings, "")
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
			return 1
		}
		name, _, err := cfg.Session.Resolve(cfg.Dir())
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
			return 1
		}
		id, ok, err := tmux.FindSessionID(runner, name)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
			return 1
		}
		if !ok {
			_, _ = fmt.Fprintf(stderr, "wyrm: no running session named %q for this folder (run it from inside the session you want to save, or start it with wyrm first)\n", name)
			return 1
		}
		sessionID, sessionName = id, name
	}

	dest := *configPath
	exists := false
	if dest == "" {
		var err error
		dest, exists, err = config.EditTarget(settings)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
			return 1
		}
	} else if _, statErr := os.Stat(dest); statErr == nil {
		exists = true
	} else if !errors.Is(statErr, os.ErrNotExist) {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+statErr.Error())
		return 1
	}
	if exists {
		_, _ = fmt.Fprintf(stderr, "wyrm: %s already exists, remove it first\n", dest)
		return 1
	}

	cfg, err := freeze.Config(runner, sessionID, sessionName, saveRoot(runner, sessionID, dest, stderr))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}

	if _, loadErr := config.Load(dest); loadErr != nil {
		_, _ = fmt.Fprintf(stderr, "wyrm: warning: %s: %v\n", dest, loadErr)
	}

	_, _ = fmt.Fprintf(stdout, "saved session %s to %s\n", sessionName, dest)
	return 0
}

// saveRoot decides what to write as the saved config's session.root.
//
// "." is preferred, because it keeps the config portable — a repo can commit
// its .wyrm.toml and every clone works. But "." is only correct when the
// session's own directory is the one the config is being written next to;
// saving session "api" into ~/web, or into the shared config directory, would
// otherwise produce a config that rebuilds the layout in the wrong place. In
// those cases write the session's real path and say so.
func saveRoot(runner tmux.Runner, sessionID, dest string, stderr io.Writer) string {
	path, err := tmux.SessionPath(runner, sessionID)
	if err != nil || path == "" {
		return "."
	}
	destDir, err := filepath.Abs(filepath.Dir(dest))
	if err != nil {
		return path
	}
	if absPath, err := filepath.Abs(path); err == nil && absPath == destDir {
		return "."
	}
	_, _ = fmt.Fprintf(stderr, "wyrm: warning: the session's directory (%s) is not where this config is being written (%s); saving an absolute session.root\n", path, destDir)
	return path
}

// runList prints the running tmux sessions non-interactively, in the given
// format — for scripts and status bars, where the interactive picker doesn't
// apply. An empty session list is not an error in any format: table mode
// reports it on stderr (matching picker.Run's message) but exits 0; json/toml
// print an empty array so consumers don't need to special-case "no server
// running".
func runList(args []string, stdout, stderr io.Writer, runner tmux.Runner) int {
	fs := newFlagSet("list", stderr)
	format := fs.String("format", "table", "output format: table, json, toml, or names")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	if code, ok := requireNoArgs(fs, stderr); !ok {
		return code
	}

	sessions, err := picker.ListSessions(runner)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	if sessions == nil {
		sessions = []picker.Session{}
	}

	switch *format {
	case "table":
		if len(sessions) == 0 {
			_, _ = fmt.Fprintln(stderr, "wyrm: no running tmux sessions")
			return 0
		}
		for _, s := range sessions {
			_, _ = fmt.Fprintln(stdout, formatSessionRow(s))
		}
	case "names":
		for _, s := range sessions {
			_, _ = fmt.Fprintln(stdout, s.Name)
		}
	case "json":
		data, err := json.MarshalIndent(sessions, "", "  ")
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
			return 1
		}
		_, _ = fmt.Fprintln(stdout, string(data))
	case "toml":
		data, err := toml.Marshal(struct {
			Sessions []picker.Session `toml:"sessions"`
		}{Sessions: sessions})
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
			return 1
		}
		_, _ = stdout.Write(data)
	default:
		// 2, like any other bad flag value — an unknown -format is a usage
		// error, not a runtime failure.
		_, _ = fmt.Fprintf(stderr, "wyrm: unknown -format %q (use table, json, toml, or names)\n", *format)
		return 2
	}
	return 0
}

// formatSessionRow renders one session as a plain, awk-able line: name,
// window count, and an attached marker — the same shape as the picker's row,
// minus color codes. See picker.FormatRow.
func formatSessionRow(s picker.Session) string {
	return picker.FormatRow(s, false)
}

// runPick lets the user choose a running session and attaches to it. An empty
// choice (nothing running, or the user aborted) exits quietly.
func runPick(args []string, stderr io.Writer, runner tmux.Runner, insideTmux func() bool, attach func(string) error) int {
	fs := newFlagSet("pick", stderr)
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	if code, ok := requireNoArgs(fs, stderr); !ok {
		return code
	}
	sessionID, err := picker.Run(runner, stderr)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	if sessionID == "" {
		return 0
	}
	return attachOrSwitch(runner, stderr, insideTmux, attach, sessionID)
}

// runTUI opens the interactive session-management TUI and, if the user chose a
// session to attach to, hands the terminal over after the alt-screen program
// exits — the same deferred-attach dance runPick uses, since a full-screen
// program can't attach in place.
func runTUI(args []string, stderr io.Writer, runner tmux.Runner, insideTmux func() bool, attach func(string) error) int {
	fs := newFlagSet("tui", stderr)
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	if code, ok := requireNoArgs(fs, stderr); !ok {
		return code
	}
	settings, code, ok := loadSettings(stderr)
	if !ok {
		return code
	}
	sessionID, err := tui.Run(runner, settings, stderr)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "wyrm: "+err.Error())
		return 1
	}
	if sessionID == "" {
		return 0
	}
	return attachOrSwitch(runner, stderr, insideTmux, attach, sessionID)
}

// attachOrSwitch hands the terminal to the session identified by sessionID
// (a tmux session ID such as "$3" — see tmux.FindSessionID for why a raw
// session name isn't used here), switching the client instead of nesting
// when wyrm is already running inside tmux.
func attachOrSwitch(runner tmux.Runner, stderr io.Writer, insideTmux func() bool, attach func(string) error, sessionID string) int {
	if insideTmux() {
		if out, err := runner.Run("switch-client", "-t", sessionID); err != nil {
			_, _ = fmt.Fprintf(stderr, "wyrm: switching to session: %v (%s)\n", err, out)
			return 1
		}
		return 0
	}

	if err := attach(sessionID); err != nil {
		_, _ = fmt.Fprintf(stderr, "wyrm: attaching to session: %v\n", err)
		return 1
	}
	return 0
}
