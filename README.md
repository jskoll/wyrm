<p align="center">
  <img src="logo.svg" alt="wyrm" width="240">
</p>

# wyrm 🐉

Repeatable tmux session layouts from a TOML config — nested split trees,
lifecycle hooks, one static binary.

Drop a `.wyrm.toml` in your project, run `wyrm`, and get the same windows,
panes, and running commands every time.

```toml
[session]
root = "."

[[windows]]
name = "code"

  [[windows.splits]]
  command = "nvim"

  [[windows.splits]]
  type = "h"          # split horizontally: new pane on the right
  size = 30           # it gets 30% of the width
  command = "npm run dev"
```

## Why another one?

| | Language | Config | Runtime deps | Layouts as split *trees* | Lifecycle hooks |
|---|---|---|---|---|---|
| tmuxinator | Ruby | YAML | Ruby | — | ✓ |
| tmuxp | Python | YAML/JSON | Python | — | ✓ |
| smug | Go | YAML | none | — | ✓ |
| **wyrm** | Go | **TOML** | none | **✓** | ✓ |

- **TOML, not YAML** — comment-friendly, no indentation traps.
- **Splits are a tree** — nest splits inside splits with explicit percentage
  sizes, instead of picking from preset layouts.
- **Pane-ID targeting** — layouts come out the same regardless of your
  `base-index` / `pane-base-index` settings.
- **One static binary** — Go stdlib plus a TOML parser, nothing at runtime
  but tmux itself.

## Install

```sh
go install github.com/jskoll/wyrm@latest
```

Or build from a clone: `make install` (uses `go install` with a stamped version).

## Usage

```sh
wyrm                       # use .wyrm.toml (or legacy .tmuxconfig) in the cwd
wyrm -config path/to/file  # explicit config
wyrm -kill                 # destroy the session (runs on_project_exit first)
wyrm -version
```

If neither `.wyrm.toml` nor `.tmuxconfig` is found, wyrm falls back to a
built-in default: a single unnamed window rooted at the current directory.

If a session with the same name is already running, wyrm **reattaches** to
it instead of rebuilding it. Otherwise it builds the session fresh, then
attaches.

Run from inside an existing tmux client, wyrm switches the client to the
session instead of nesting one tmux inside another.

## Config reference

### `[session]`

| Key | Type | Default | Description |
|---|---|---|---|
| `name` | string | basename of `root` | tmux session name |
| `root` | string | `.` | Working directory for every window; `$VAR` is expanded |
| `on_project_start` | string | — | Shell command run (via `bash -c`, in `root`) before the session is created |
| `on_project_exit` | string | — | Shell command run before `wyrm -kill` destroys the session |
| `startup_window` | string | first window | Window (name or index) to focus after creation |
| `startup_pane` | int | — | Pane to focus within `startup_window` (uses your `pane-base-index`) |

At least one of `name` / `root` is required.

### `[[windows]]`

| Key | Type | Default | Description |
|---|---|---|---|
| `name` | string | — | Window name |
| `pre_window` | string | — | Command typed into **every pane** before its own command (e.g. `nvm use 18`) |
| `splits` | list | — | Split tree (below) — the recommended layout format |
| `panes` | list | — | Legacy flat pane list (below); ignored when `splits` is set |
| `layout` | string | `tiled` | tmux layout applied after legacy `panes` (`even-horizontal`, `main-vertical`, ...) |

### `[[windows.splits]]` — the split tree

| Key | Type | Default | Description |
|---|---|---|---|
| `type` | string | — | `h`/`horizontal` or `v`/`vertical`. **Omit** to target the pane created by the previous entry (or the window's first pane) without splitting |
| `size` | int | tmux default | Percentage of space given to the new pane (1–99) |
| `command` | string | — | Typed into the pane; entries starting with `#` are comments and skipped |
| `children` | list | — | Nested splits, applied inside this entry's pane |

How the tree is walked: each entry with a `type` splits the pane of the
previous entry at the same level (the window's initial pane for the first
entry). `children` do the same, starting from their parent's pane.

```toml
[[windows]]
name = "dev"

  [[windows.splits]]
  command = "nvim"            # window's first pane

  [[windows.splits]]
  type = "h"                  # split it: new right-hand pane, 30%
  size = 30
  command = "npm run dev"

    [[windows.splits.children]]
    type = "v"                # split the right-hand pane: bottom half
    size = 50
    command = "npm test -- --watch"
```

### `[[windows.panes]]` — legacy flat list

```toml
[[windows]]
name = "tests"
layout = "even-horizontal"

[[windows.panes]]
command = "npm test -- --watch"

[[windows.panes]]
command = "# scratch"          # comment: pane is created, nothing runs
```

Panes split alternately h/v, then `layout` (default `tiled`) evens them out.

More in [`examples/`](https://github.com/jskoll/wyrm/tree/main/examples):
minimal, Node.js, PHP/Symfony, Python, nested splits.

## Security

A wyrm config **executes shell commands by design** — hooks run via
`bash -c`, and pane commands are typed into your shell. Treat config files
with the same trust as a `Makefile` or `.envrc`: don't run one you haven't
read.

## Development

```sh
make build       # build ./wyrm
make test        # unit + integration (integration needs tmux; isolated socket)
make test-unit   # -short: unit tests only
make lint        # golangci-lint + gofmt
```

See [CONTRIBUTING.md](https://github.com/jskoll/wyrm/blob/main/CONTRIBUTING.md)
for the layout and error-handling conventions.

## License

[MIT](LICENSE)
