# Open-Sourcing Plan: tmux-session → its own repo

> Extracting `tools/tmux-session` from dragon-cli into a standalone open-source project.
> Status: **plan only — nothing executed yet.**

---

## 1. What it is, and why it's worth open-sourcing

A single-binary Go CLI that creates repeatable tmux session layouts from a TOML config:
nested split trees with explicit sizes, per-window setup commands (`pre_window`),
lifecycle hooks (`on_project_start` / `on_project_exit`), startup window/pane targeting,
and auto-attach (or `switch-client` when already inside tmux).

**Positioning vs. existing tools:**

| Tool | Language | Config | Runtime deps | Notes |
|---|---|---|---|---|
| tmuxinator | Ruby | YAML | Ruby gem env | The incumbent; heavy install |
| tmuxp | Python | YAML/JSON | Python env | Feature-rich, complex |
| smug | Go | YAML | none | Closest competitor |
| sesh | Go | TOML | none | Session *picker*, not layout definitions |
| **this tool** | Go | TOML | none | Nested split trees w/ sizes, lifecycle hooks, `pre_window` |

The differentiators to lead with in the README: **TOML** (comment-friendly, no YAML
indentation traps), **recursive split trees with explicit percentage sizes** (none of the
above model layouts as a tree), and **lifecycle hooks** — all in one static binary.

---

## 2. Name

`tmux-session` is too generic to search for, and every name in this space is crowded.
Availability checked via `brew search` (2026-07-04):

| Candidate | brew | Notes |
|---|---|---|
| **wyrm** ✅ recommended | free | Dragon-themed (nod to dragon-cli origins), short, memorable, easy to say and type. `wyrm up`, `wyrm kill` read well if subcommands come later. |
| lair | free | "Your project's lair" — good metaphor; minor collision with Holochain's `lair-keystore` |
| den | free | Shortest, but a generic English word — near-unsearchable |
| sesh | **taken** | Existing Go tmux session manager — avoid |

**Recommendation: `wyrm`.**

Before creating the repo, verify: GitHub repo/org search, `brew search wyrm` again,
crates.io / npm (squatters cause confusion even for a Go tool), optionally a domain.

Config file naming: default to `.wyrm.toml`, but **keep `.tmuxconfig` as a recognized
fallback** so existing dragon-cli users (and the repo's own `.tmuxconfig`) keep working.

---

## 3. Migration steps

### 3.1 Extract with history (recommended)

The tool's history is worth keeping (shows real evolution), but dragon-cli's history
contains the formerly-committed 3.7MB binary — strip it or the new repo inherits
~50MB of dead blobs:

```sh
brew install git-filter-repo
git clone git@github.com:jskoll/dragon-cli.git wyrm-extract && cd wyrm-extract
git filter-repo \
  --subdirectory-filter tools/tmux-session \
  --strip-blobs-bigger-than 1M
# result: repo rooted at the tool, binary blobs gone, dragon-cli history preserved
```

Fallback if the filtered history reads too dragon-cli-specific: fresh repo, single
"initial import" commit. Less interesting, zero baggage.

### 3.2 New repo setup

1. Create `github.com/jskoll/wyrm` (public, description, topics: `tmux`,
   `session-manager`, `go`, `cli`, `terminal`, `developer-tools`).
2. Update `go.mod` module path to `github.com/jskoll/wyrm`; rename binary.
3. Push, enable branch protection on `main`, enable Discussions (optional).

### 3.3 dragon-cli follow-up (after the new repo has a release)

- Delete `tools/tmux-session/`.
- `install.sh` step 12: replace the local `go build` with `go install
  github.com/jskoll/wyrm@latest` (or `brew install jskoll/tap/wyrm` once the tap exists).
- Update `README.md`, `docs/tmux-cheatsheet.md`, `docs/structure.md`, `docs/index.md`
  references; root `.tmuxconfig` keeps working via the fallback name.
- `scripts/export-session.sh` stays in dragon-cli for now (see roadmap — it wants to
  become `wyrm export`).

---

## 4. Code quality for open source

Current state: one ~300-line `main.go`, builds clean with `gofmt`/`go vet`, only dep is
`go-toml/v2`. Good bones, but single-user assumptions remain.

### 4.1 Must-fix before v0.1.0 (correctness for other people's machines)

- **Pane targeting by ID, not index.** `processSplitTree` / `createPanesFromSplits`
  address panes as `window.0`, `window.1`… which assumes the user's `pane-base-index`.
  Capture real IDs instead: `tmux split-window -P -F '#{pane_id}'` returns `%N`, which is
  stable regardless of indexing settings. (Window indexing was already fixed this way.)
- **Remove or justify the `time.Sleep(50ms)` calls.** tmux commands are synchronous over
  the control socket; the sleeps almost certainly paper over nothing. Verify by removing
  them and running the integration suite.
- **Defined error policy**, documented in CONTRIBUTING: fail-fast (`log.Fatalf`) for
  structural failures (can't create session/window), warn-and-continue for per-pane
  command failures. That's the current behavior — make it a stated contract.
- **`-kill` shouldn't run `on_project_exit` when the session doesn't exist** (hook
  currently fires before the existence check).

### 4.2 Structure

```
wyrm/
├── main.go               # flags + wiring only
├── internal/config/      # TOML types, parsing, validation, name derivation
├── internal/tmux/        # Runner interface + real exec implementation
├── examples/             # nodejs, php-symfony, python + a minimal starter
└── testdata/             # good/bad config fixtures
```

The `tmux.Runner` interface is the key move: unit tests mock it to assert the exact
command sequences for a given config, without tmux installed.

### 4.3 Tests

- **Unit** (no tmux needed): config parsing (valid/invalid TOML, defaults, session-name
  derivation from root, `startup_pane` nil-vs-0), split-type normalization, full
  split-tree walk against the mocked Runner.
- **Integration** (CI has tmux): isolated server per test — `tmux -L wyrm-test-$$` —
  create sessions from the example configs, assert windows/panes/layout counts via
  `list-windows`/`list-panes`, exercise `-kill` and both hook paths, then `kill-server`.
  These caught real bugs during dragon-cli development; they're the highest-value tests.

### 4.4 Tooling & CI/CD

- `golangci-lint` with a committed config; `Makefile` (or `justfile`) with
  `build` / `test` / `lint` / `install` targets.
- GitHub Actions: test matrix on `ubuntu-latest` + `macos-latest` (tmux installs cleanly
  on both); lint job; runs on PRs and main.
- **goreleaser** on tags: darwin/linux × amd64/arm64 binaries, checksums, GitHub
  Release, and a Homebrew tap (`jskoll/homebrew-tap`) so `brew install jskoll/tap/wyrm`
  works day one.
- `-version` flag injected via ldflags; semver starting at `v0.1.0`.

---

## 5. Documentation

The existing `tools/tmux-session/README.md` is a solid base — restructure rather than
rewrite:

- **README.md** — badges (CI, release, license); one-paragraph pitch + the comparison
  table from §1; a **demo GIF** (use [vhs](https://github.com/charmbracelet/vhs) — script
  is committed, so the GIF regenerates reproducibly); install (brew tap, `go install`,
  release binaries); 60-second quickstart; then a **complete config reference table**:
  every key with type, default, and one-line description (session.*, windows[].*,
  splits[].*, panes[].*).
- **Security note, prominent**: config files execute shell commands by design (hooks,
  pane commands). Same trust model as a Makefile or `.envrc` — only run configs you
  trust. Saying this explicitly preempts the inevitable "arbitrary code execution!" issue.
- **CHANGELOG.md** — keep-a-changelog format, from v0.1.0.
- **CONTRIBUTING.md** — dev setup, how to run unit vs integration tests, the error-policy
  contract, conventional commits.
- **CODE_OF_CONDUCT.md** — Contributor Covenant, stock.
- **.github/** — issue templates (bug: include `tmux -V`, OS, config snippet; feature),
  PR template, `SECURITY.md` (report via GitHub private advisories).
- **examples/** — keep the three project templates, add a minimal 5-line starter.

---

## 6. License

**Recommendation: MIT.**

- The norm for this exact niche (lazygit, fzf, smug, sesh are all MIT) — contributors
  and packagers expect it, corporate users need no review.
- Only dependency is `go-toml/v2` (MIT) — fully compatible.
- Apache-2.0 is the alternative if an explicit patent grant ever mattered; for a tmux
  layout tool it doesn't, and MIT's brevity wins.

Copyright line: `Copyright (c) 2026 Jason Skollingsberg`. Add the `LICENSE` file in the
very first commit of the new repo — license-from-birth avoids any ambiguity about
pre-license commits.

---

## 7. Launch checklist

1. Repo created, code moved, module renamed, LICENSE in first commit
2. CI green (unit + integration, both OSes), lint clean
3. README with GIF + config reference; CHANGELOG; CONTRIBUTING; CoC; templates
4. Must-fix items from §4.1 done
5. Tag `v0.1.0` → goreleaser publishes binaries + brew tap
6. dragon-cli switched over (§3.3) — dogfooding the public artifact
7. Announce: PR to [awesome-tmux](https://github.com/rothgar/awesome-tmux),
   r/tmux, r/commandline, lobste.rs, Show HN (only after a week of dogfooding)

## 8. Roadmap (post-launch ideas, seeded as GitHub issues)

- `wyrm export` — absorb dragon-cli's `scripts/export-session.sh` (dump a running
  session to config) as a native subcommand
- `wyrm list` / `wyrm attach` — list configs/sessions, attach-if-exists instead of
  kill-and-recreate
- `--dry-run` — print the tmux commands without executing (great for debugging configs)
- Config schema validation with helpful errors (unknown keys currently ignored silently)
- Windows: explicit "not supported, WSL works" statement
