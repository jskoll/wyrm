# Contributing

Thanks for taking an interest! This is a small, focused tool — issues and PRs
are welcome.

## Development setup

Requirements: the Go version in `go.mod` (currently 1.24) or newer, tmux 3.1+
(for integration tests; 3.1 is the floor for `split-window -l N%`), and
optionally [golangci-lint](https://golangci-lint.run/).

```sh
git clone https://github.com/jskoll/wyrm && cd wyrm
make build       # ./wyrm
make test        # unit + integration
make test-unit   # unit only (no tmux needed)
make lint
```

The integration tests run a real tmux server on an isolated socket
(`tmux -L wyrm-it-<pid>`) — they never touch your own sessions.

## Layout

```
main.go            subcommand dispatch + wiring only
internal/config/   TOML types, parsing, validation, settings, project discovery
internal/tmux/     Runner interface, real exec implementation, dry-run recorder
internal/session/  session creation/teardown (tested against a mock Runner)
internal/freeze/   the reverse of session: a live tmux layout -> a config
internal/picker/   the dependency-free raw-terminal fuzzy picker (wyrm pick)
internal/tui/      the Bubble Tea session manager (wyrm tui)
```

Everything that talks to tmux goes through `tmux.Runner`, so it can be driven
by a recording mock in tests. `main.run` takes its stdio, runner, and attach
function as parameters for the same reason — the whole CLI is testable without
a tmux server.

New behavior should come with a unit test against the mocked `tmux.Runner`
(assert the exact command sequence) and, where it changes real tmux
interaction, an integration test assertion.

## Error-handling contract

- **Structural failures** — can't parse the config, create the session, or
  create a window — return an error; the CLI exits non-zero.
- **Per-pane failures** — a split or command that fails — print a warning to
  stderr and continue, so one broken command doesn't abort the rest of the
  layout.

Keep changes consistent with this split.

## Commits

Conventional commits, please: `feat: ...`, `fix: ...`, `docs: ...`,
`test: ...`, `chore: ...`. Keep PRs focused on one change.

## Docs site

The published docs under `docs/` are built with [mkdocs-material](https://squidfunk.github.io/mkdocs-material/):

```sh
pip install -r requirements-docs.txt
make docs-serve   # live preview at http://127.0.0.1:8000
make docs-build   # build the static site into site/
```
