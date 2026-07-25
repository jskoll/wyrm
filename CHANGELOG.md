# Changelog

All notable changes to this project will be documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added
- `wyrm restart` stops the session and builds it again from the current
  config — previously `wyrm kill && wyrm`.
- `wyrm <name>` now falls back to *starting* a known project when no session
  by that name is running, looking through the local and shared configs. This
  is what makes `storage = "shared"` useful: any project can be started from
  anywhere, without `cd`-ing to it first.
- `wyrm kill <name>` kills a session by name, running its `on_project_exit`
  hook when wyrm can find the matching config. Previously `kill` could only
  target the current folder's session.
- `wyrm up -n` prints the tmux commands a build would issue, without running
  any of them or consulting a running session.
- `wyrm` reports which config it resolved (on stderr) before building.
  Discovery has five layers; an unexpected session was otherwise unexplainable.
- Config validation now emits warnings for config that builds but probably
  doesn't do what was meant: `layout` set alongside `splits` (it's ignored),
  `splits` and `panes` both set, a first split entry with a `type` (it leaves
  the window's initial pane empty), and use of the deprecated flat `panes`
  list or the `.tmuxconfig` filename.
- The TUI gained `/` to filter the focused panel, `PgUp`/`PgDn`/`g`/`G`
  navigation, and a 3-second refresh of the project and session lists.
- `wyrm save -config PATH` writes to PATH instead of the resolved location,
  for symmetry with `up`/`kill`/`validate`/`edit`.
- A mistyped subcommand that falls through to attach-by-name and finds no
  matching session now hints at the nearest real subcommand, e.g.
  `wyrm klil` suggests `kill`.

### Changed
- **BREAKING:** nested splits are now created breadth-first — every entry at a
  level is created before wyrm descends into any of their `children`. A
  `size` is therefore a share of the space its own level was given, rather
  than of whatever an earlier sibling's children happened to leave behind.
  This makes `wyrm save` → `wyrm` a faithful round trip for nested layouts,
  which it was not: a container that wasn't the last sibling rebuilt to a
  visibly different window. Hand-written configs that were tuned against the
  old sequential behavior may need their `size` values revisited.
- **BREAKING:** `session.root` now expands a leading `~`, and errors on an
  undefined `$VAR` instead of silently expanding it to an empty string.
  `root = "~/code/app"` previously produced a literal directory named `~`,
  and `root = "$UNSET/api"` silently became `/api`.
- **BREAKING:** a relative `session.root` resolves against the directory the
  config was loaded from, not the process's working directory. Starting a
  project from the TUI (or now `wyrm <name>`) rooted the session wherever the
  user happened to be standing rather than at the project.
- **BREAKING:** a config with no `[[windows]]` is now a validation error.
  `wyrm validate` used to bless configs that `wyrm` would then refuse.
- **BREAKING:** `wyrm list` with an unknown `-format` exits 2, like any other
  bad flag value, rather than 1.
- Subcommands now reject stray positional arguments. `wyrm list json` — which
  looks like it should work — silently printed the default table format.
- `wyrm pick`: `Ctrl-X` now asks for confirmation before killing a session,
  matching the TUI. `Delete` no longer kills a session at all: it was
  unconfirmed, undocumented, and sat one key away from Home and End.
- **BREAKING:** the CLI moved from top-level flags to git-style subcommands.
  `wyrm -kill` → `wyrm kill`, `-pick` → `pick`, `-tui` → `tui`, `-save` →
  `save`, `-edit` → `edit`, `-validate` → `validate`, `-list` → `list`,
  `-list-configs` → `list-configs`, `-migrate-config` → `migrate-config`,
  `-version` → `version`. Per-mode flags now sit on their subcommand
  (`wyrm kill -config PATH`, `wyrm list -format json`). Bare `wyrm`,
  `wyrm <name>`, and `wyrm -config PATH` are unchanged, and a new `wyrm up`
  spells out the default build/attach. As a side effect, modes can no longer
  be combined or silently ignored — each subcommand parses only its own flags,
  and `wyrm help` / `wyrm <cmd> -h` document them.
- Lifecycle hooks (`on_project_start` / `on_project_exit`) now run via `$SHELL`
  (falling back to `sh`) instead of a hardcoded `bash`, so they work on systems
  without bash and honor the user's shell.
- Each subcommand's `-h` now prints its one-line synopsis before its flags,
  instead of a bare stdlib flag dump with no context.
- `wyrm version`'s unstamped-build format changed from `dev (rev)` to
  `dev+rev`, matching semver's `+build-metadata` convention so tooling that
  greps for a semver-ish token isn't thrown by the parentheses.

### Fixed
- A parse error in the user's `~/.tmux.conf` no longer corrupts the session.
  `new-session` is the command that starts the tmux server, so the server's
  config errors landed on stderr at exactly the moment wyrm was parsing that
  command's `-F` output — and the field count still matched, so the check
  passed and every later command targeted a session ID with a diagnostic glued
  to the front of it. wyrm now reads stdout only, and validates every tmux
  object ID it parses.
- A freshly built session opens on its **first** window, focused on that
  window's first pane, as documented. Windows and splits were created without
  `-d`, so tmux made each new one current and the session landed on the *last*
  window in the config.
- `pre_window` now runs exactly once in every pane of the window, as
  documented. It was emitted per split-tree *entry*, which both skipped panes
  (a first entry with a `type` left the window's initial pane untouched) and
  repeated itself (a nested container reuses its parent's pane, so a two-level
  nest typed it twice).
- Pane commands are sent with `send-keys -l --`, so a command that happens to
  be a tmux key name is typed rather than pressed. `command = "up"` used to
  press the Up arrow and Enter, re-running the previous shell history entry;
  a command starting with `-` was parsed as a flag.
- A build that fails partway through now tears the half-built session down.
  It used to be left running, so the next `wyrm` found it, reported "already
  running, attaching", and handed over a session missing most of its windows
  with no sign anything had gone wrong.
- wyrm now reports the session name tmux actually assigned. Some tmux builds
  rewrite `.` and `:` to `_`, so a project in a directory called `example.com`
  became the session `example_com` — after which the next run couldn't find
  it, tried to create a duplicate, and failed, and `wyrm kill` could never
  find it at all. Session lookup also falls back to the sanitized form.
- `wyrm save` no longer records an idle shell as a pane's command. Replaying
  `zsh` into a shell nested a second one in every idle pane, so leaving the
  session took two exits per pane.
- `wyrm save` writes an absolute `session.root` (and says so) when the
  session's own directory isn't where the config is being written, instead of
  always writing `.`.
- The shared config directory now honors `$XDG_CONFIG_HOME`, like the settings
  file next to it. The default was hardcoded to `~/.config/wyrm/settings`, so
  a user with `XDG_CONFIG_HOME` set had wyrm read settings from one place and
  look for shared configs in another.
- `wyrm tui`: the selected row is highlighted across its whole width. lipgloss
  ends every styled run with a full SGR reset and doesn't restore the outer
  style, so wrapping a row in reverse-video switched it off at the first
  colored span — the window index was highlighted and the name next to it
  wasn't, on every row of the Windows and Panes panels.
- `wyrm tui`: failed actions are reported in the footer. Errors rendered only
  when the preview happened to be empty — which in normal use it never is — so
  a failed kill, rename, or new-window looked identical to a successful one.
- `wyrm tui`: the pane preview shows the *end* of the pane's visible region
  rather than the beginning, so the prompt and latest output are visible.
- `wyrm tui`: terminals between 8 and 16 rows no longer render more lines than
  the screen has, which silently sliced the top panels away — including in the
  `display-popup -h 80%` recipe the README suggests. The minimum size is now
  enforced and reported accurately.
- `wyrm tui`: panel heights are allocated by content instead of in equal
  quarters, which gave the Panes panel as much room as Sessions and left every
  list showing two rows at a time in an 80x24 terminal.
- `wyrm tui`: the selection is visible in unfocused panels, so the session and
  window a pane belongs to stay readable while the cursor is elsewhere.
- `wyrm tui`: `n`, `z`, and `L` only act on the panels whose footer advertises
  them; they previously fired from any panel, acting on a selection in a panel
  that didn't have focus.
- `wyrm tui`: `Enter` no longer confirms a kill — `x` followed by a reflexive
  `Enter` destroyed a session. Only `y` confirms.
- `wyrm tui`: the layout cycle (`L`) restarts per window, so the first press
  on a window always changes something. The rename prompt has a width, so a
  long name no longer scrolls the cursor off the screen.
- `wyrm pick`: `SIGTERM`/`SIGHUP` restore the terminal instead of leaving it in
  raw mode with the cursor hidden — reachable via `pkill wyrm`, closing the
  terminal, or killing the tmux pane it ran in. `SIGWINCH` now redraws.
- `wyrm pick`: navigation keys no longer leak into the fuzzy filter. Only the
  first byte of an escape sequence was consumed, so Home, End, PgUp, and PgDn
  each typed a `~`, and a shifted arrow typed `;2A`.
- `wyrm pick`: session names are padded and truncated by display width rather
  than rune count, so a CJK or emoji name doesn't break the column alignment
  and a long name no longer runs over the window count and attached marker.
- `wyrm edit -config new/dir/x.toml` creates the parent directory, as the
  flagless form already did, instead of handing the editor an unwritable path.
- `wyrm edit` returns 1 rather than 255 when the editor is killed by a signal.
- Lifecycle hook output is streamed to stderr and the hook is announced before
  it runs. Output was captured and discarded unless the hook failed, so a slow
  `on_project_start` blocked wyrm behind a blank screen.
- `startup_window` / `startup_pane` are now resolved to tmux object IDs
  (`@window`, `%pane`) via `list-windows`/`list-panes` before being targeted,
  instead of being interpolated into a `session:window.pane` string. A window
  name containing `.` (e.g. `app.web`) was previously misparsed by tmux's `-t`
  syntax — the same hazard already guarded against for session names.
- `wyrm tui`: pane-preview colors are preserved (`capture-pane -e`), so the
  live preview reflects the pane's real colors instead of flattening them.

### Deprecated
- The flat `[[windows.panes]]` list and the `.tmuxconfig` filename are slated
  for removal in 1.0. Use the `splits` tree and `.wyrm.toml`; `wyrm save` only
  emits `splits`.

### Internal
- Project discovery moved into `internal/config` and is now shared by the TUI's
  Projects panel and `wyrm <name>` instead of living only in the TUI.
- The "no server running" probe and the tmux ID shapes are defined once in
  `internal/tmux`, rather than duplicated between it and `internal/picker`.
- CI runs `-race`, reports coverage, and builds the release artifacts on every
  PR so a broken `.goreleaser.yaml` is caught before tag time.
- New tests cover the areas that couldn't previously fail: pane *geometry* for
  nested splits (including a `save` → `up` round trip against a real tmux),
  the selection highlight (rendered with a forced color profile, since lipgloss
  degrades to plain text in a non-TTY test process), and the session-level
  kill/rename actions, which had no coverage anywhere.
- The three shipped examples that taught the deprecated flat `panes` format
  now use the `splits` tree; `basic.wyrm.toml` keeps it as a labelled
  reference. A test loads every shipped example so they can't rot.
- Shell completions (bash/zsh/fish) rewritten for the subcommand surface.
- Added Dependabot (Go modules + GitHub Actions, monthly, grouped).
- `wyrm version` on an unstamped `go install` now reports the VCS revision from
  the build info instead of a bare `dev`.

## [0.2.1] - 2026-07-24

### Fixed
- `wyrm -tui`: the `?` help overlay no longer runs off the top of the screen
  when it's taller than the terminal (only the bottom was visible, with no way
  to reach the rest). It now scrolls — `↑`/`↓` or `j`/`k`, `PgUp`/`PgDn`,
  `g`/`G`, `Esc` to close — with a position indicator, and lays its sections out
  in two columns (collapsing to one on a narrow terminal) so it fits without
  scrolling in more cases.

## [0.2.0] - 2026-07-24

### Added
- `wyrm -tui`: a full-screen, keyboard-driven session manager in the spirit of
  lazygit. Four stacked panels — Projects, Sessions, Windows, Panes — drive a
  live preview of the selected pane (`capture-pane`, refreshed each second), or
  the selected config's contents on the Projects panel. Navigate with
  `Tab`/`1`-`4` and `j`/`k`; `Enter` attaches, landing on the exact window/pane
  under the cursor (or starts/attaches a project's session). Manage tmux
  directly: `x` kills the focused session/window/pane (with a confirm), `r`
  renames, `n` opens a new window, `L` cycles the window layout, `z` toggles
  pane zoom. On the Projects panel, `e` edits a config in `$EDITOR` and `x`
  stops a session running its `on_project_exit` hook. Press `?` for a
  full-screen overlay of every binding. Like `-pick`, it uses `switch-client`
  inside tmux and `attach-session` otherwise, and suppresses the preview of the
  pane it is itself running in. Shell completion now offers `-tui`.

## [0.1.12] - 2026-07-24

### Fixed
- `wyrm -pick`: fixed the picker leaving a trail of stale header lines that
  marched down the screen as you navigated inside a narrow tmux
  `display-popup`. An over-long row (such as the footer) wrapped onto a
  second physical line, desyncing the renderer's line count from the rows on
  screen so each redraw's cursor reposition undershot. The picker now
  disables terminal autowrap while running, clipping long rows at the right
  margin instead. This is the real cause of the popup rendering issue 0.1.11
  attempted to address; the synchronized-update (DEC 2026) change from 0.1.11
  is reverted, as it did not fix the problem.

## [0.1.11] - 2026-07-24

### Fixed
- `wyrm -pick`: fixed screen jitter when the session list is taller than
  the picker's viewport, most visible inside a small tmux `display-popup`.
  The frame now reserves the terminal's bottom row so its trailing newline
  no longer scrolls the display on every keypress, and each repaint is
  wrapped in synchronized-update mode (DEC 2026) so it presents atomically.

## [0.1.10] - 2026-07-18

### Added
- `wyrm -pick`: `Ctrl-W` on a selected session shows its windows (names
  only) so you can jump straight to one — `Enter` selects a window and
  attaches or switches directly to it, `Esc` backs out to the session list.
  `Ctrl-C` now always quits the picker outright, from either view.

## [0.1.9] - 2026-07-18

### Added
- `wyrm -save`: snapshot a running tmux session's windows, split layout,
  and sizes into a new config for the current folder — the reverse of
  building a session from one. Each split's `command` is captured from
  whatever program is currently running in that pane's foreground, since
  tmux keeps no record of what was originally typed. Like
  `-migrate-config`, it refuses to overwrite an existing config.

### Changed
- A bare `wyrm` with no config file now always builds (or attaches to) a
  session for the current folder, even if unrelated tmux sessions are
  already running elsewhere. Previously it opened the interactive picker
  instead whenever *any* session was running; that's now only triggered
  explicitly via `-pick`.

## [0.1.8] - 2026-07-13

### Added
- Dynamic shell completion for bash, zsh, and fish (`completions/`),
  installed automatically by the Homebrew formula. Completes flag names,
  `-format`'s values, `-config` (real local/shared config paths), and a
  bare argument (real running session names).
- `wyrm <name>`: attach or switch directly to a running session by exact
  name, without the interactive picker — what shell completion completes a
  bare argument to.
- `-list -format names`: bare newline-separated session names, for
  completion and scripting (e.g. piping into `fzf`).
- `-list-configs`: list candidate config file paths (local + shared
  directory), regardless of the current `storage` setting.

## [0.1.7] - 2026-07-13

### Added
- Color in `wyrm -pick`: window counts in cyan, `(attached)` in green.
  Respects [`NO_COLOR`](https://no-color.org) to disable it.

## [0.1.6] - 2026-07-12

### Fixed
- CI: fixed a lint error (unused test parameter) and made the dotted-
  session-name integration tests added in 0.1.5 tolerant of tmux builds
  that sanitize or reject `.` in session names outright instead of
  preserving it, rather than failing on them. No functional changes; this
  release is otherwise identical to 0.1.5.

## [0.1.5] - 2026-07-12

### Added
- `wyrm -edit`: open the resolved config (local, shared, or `-config`) in
  `$EDITOR`, creating one at the right location for your `storage` setting
  if none exists yet. Warns (without failing) if the saved file doesn't
  validate.
- `wyrm -validate`: check that the effective config parses and validates,
  without building a session — useful in CI or a pre-commit hook.
- `wyrm -list` (`-format table|json|toml`): print running tmux sessions
  non-interactively, for scripts and status bars, as an alternative to the
  interactive `-pick` UI.

### Fixed
- Creating, killing, attaching to, or switching to a session whose name
  contains a `.` (e.g. `wyrm.vim`) could fail outright: tmux's `-t` target
  syntax uses `.` as the window.pane separator, so such names were
  misparsed by `has-session`, `new-window`, `kill-session`, and
  `attach-session` alike — even with an `=` exact-match prefix, which only
  guards against prefix ambiguity, not this. `wyrm` now looks up and targets
  every session by its stable tmux session ID instead of its name, which
  sidesteps the issue entirely.

## [0.1.4] - 2026-07-12

### Added
- Shared config storage: set `storage = "shared"` in a new global settings
  file (`~/.config/wyrm/config.toml`) to keep project configs in one
  directory (default `~/.config/wyrm/settings`) instead of `.wyrm.toml` next
  to each project, named `<folderName>.wyrm.toml`. `wyrm -migrate-config`
  moves an existing local config there.
- Custom default config: drop a `default.wyrm.toml` next to the global
  settings file (`~/.config/wyrm/default.wyrm.toml`) to replace wyrm's
  built-in fallback used when no project config is found.

## [0.1.3] - 2026-07-12

### Added
- `wyrm -pick`: an interactive fuzzy picker for running tmux sessions. Type to
  filter, arrow keys (or Ctrl-N/Ctrl-P) to move, Enter to attach (or
  `switch-client` when already inside tmux), Ctrl-X to kill the highlighted
  session, Esc/Ctrl-C to cancel. Running bare `wyrm` in a directory with no
  config also opens the picker when sessions are already running. The picker is
  built in — no fzf or other runtime dependency.

## [0.1.2] - 2026-07-11

### Fixed
- Release pipeline: the Homebrew formula publish to `jskoll/homebrew-tap`
  was failing (invalid tap token, and CI actions pinned to the deprecated
  Node 20 runtime). Both are fixed; this release is otherwise identical to
  0.1.1.

## [0.1.1] - 2026-07-11

### Added
- Homebrew tap: `brew install jskoll/tap/wyrm` (goreleaser publishes the
  formula to `jskoll/homebrew-tap` on each release).

## [0.1.0] - 2026-07-11

### Added
- `.wyrm.toml` as the default config name; the original `.tmuxconfig` still
  works as a fallback.
- `-version` flag.
- Config validation with helpful errors (unknown split types, out-of-range
  sizes).
- Unit test suite (mocked tmux runner) and integration tests against a real
  tmux server on an isolated socket.
- CI (GitHub Actions, macOS + Linux), golangci-lint, goreleaser config.

### Changed
- Run from inside an existing tmux client, `wyrm` now switches the client to
  the target session instead of nesting one tmux inside another.
- Creating a session now **reattaches** to an existing session with the same
  name instead of killing and rebuilding it.
- When no `.wyrm.toml` or `.tmuxconfig` is found, wyrm falls back to a
  built-in default config instead of erroring out.
- Panes are targeted by tmux pane ID (`%N`) instead of index, so layouts no
  longer depend on the user's `base-index` / `pane-base-index` settings.
- `pre_window` runs in every pane before its command (as documented), not
  just the first.
- Split-tree semantics defined precisely: each entry splits the pane of the
  previous entry at its level; children work within their parent's pane.
- Structural failures exit with an error; per-pane failures warn and
  continue (now a stated contract).

### Removed
- Arbitrary `time.Sleep` synchronization (tmux commands are synchronous).
- Config-path allowlist validation — configs execute commands by design, so
  restricting their location added friction without security (see README).

### Fixed
- `wyrm -kill` no longer runs `on_project_exit` when the session isn't
  running.

[Unreleased]: https://github.com/jskoll/wyrm/compare/v0.2.1...HEAD
[0.2.1]: https://github.com/jskoll/wyrm/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/jskoll/wyrm/compare/v0.1.12...v0.2.0
[0.1.12]: https://github.com/jskoll/wyrm/compare/v0.1.11...v0.1.12
[0.1.11]: https://github.com/jskoll/wyrm/compare/v0.1.10...v0.1.11
[0.1.10]: https://github.com/jskoll/wyrm/compare/v0.1.9...v0.1.10
[0.1.9]: https://github.com/jskoll/wyrm/compare/v0.1.8...v0.1.9
[0.1.8]: https://github.com/jskoll/wyrm/compare/v0.1.7...v0.1.8
[0.1.7]: https://github.com/jskoll/wyrm/compare/v0.1.6...v0.1.7
[0.1.6]: https://github.com/jskoll/wyrm/compare/v0.1.5...v0.1.6
[0.1.5]: https://github.com/jskoll/wyrm/compare/v0.1.4...v0.1.5
[0.1.4]: https://github.com/jskoll/wyrm/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/jskoll/wyrm/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/jskoll/wyrm/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/jskoll/wyrm/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/jskoll/wyrm/releases/tag/v0.1.0
