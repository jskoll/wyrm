# Changelog

All notable changes to this project will be documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

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

[Unreleased]: https://github.com/jskoll/wyrm/compare/v0.1.10...HEAD
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
