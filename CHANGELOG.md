# Changelog

All notable changes to this project will be documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

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

[Unreleased]: https://github.com/jskoll/wyrm/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/jskoll/wyrm/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/jskoll/wyrm/releases/tag/v0.1.0
