# Contributing

Thanks for taking an interest! This is a small, focused tool — issues and PRs
are welcome.

## Development setup

Requirements: Go 1.21+, tmux (for integration tests), and optionally
[golangci-lint](https://golangci-lint.run/).

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
main.go            flags + wiring only
internal/config/   TOML types, parsing, validation
internal/tmux/     Runner interface + real exec implementation
internal/session/  session creation/teardown logic (tested against a mock Runner)
```

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
