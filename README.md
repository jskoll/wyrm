# tmux-session — Lightweight Session Configuration Tool

A minimal Go CLI for managing repeatable tmux session layouts without Ruby dependencies.

## Installation

```bash
go build -o ~/bin/tmux-session
```

Or via `dragon-cli` install script (wires into Homebrew).

## Usage

### Create a session from config

```bash
# Uses .tmuxconfig in current directory
tmux-session

# Or specify config path
tmux-session -config path/to/.tmuxconfig
```

### Kill a session

```bash
tmux-session -kill -config .tmuxconfig
```

## Config Format

`.tmuxconfig` is TOML with two main sections:

```toml
[session]
name = "myproject"               # session name (defaults to directory name)
root = "."                       # working directory (expanded via $HOME, etc.)
on_project_start = "git pull"    # optional: runs before session creation
on_project_exit = "npm run cleanup"  # optional: runs when session is killed
startup_window = "editor"        # optional: focus this window on startup (name or index)
startup_pane = 0                 # optional: focus this pane within startup_window (0-indexed)

[[windows]]
name = "editor"
pre_window = "nvm use 16"        # optional: runs before any pane commands
```

### Nested Splits (Recommended)

Define pane layout with tree-like nesting. Supports both short (`h`, `v`) and long (`horizontal`, `vertical`) split types:

```toml
[[windows]]
name = "editor"

  [[windows.splits]]
  type = "h"            # horizontal split (or "horizontal")
  size = 70             # percentage of available space
  command = "nvim"      # optional command to run

    [[windows.splits.children]]
    type = "v"          # nested vertical split (or "vertical")
    size = 50
    command = "npm test -- --watch"

    [[windows.splits.children]]
    type = "v"
    command = "# logs"  # comments don't execute
```

### Legacy Panes Format

Alternative: simple list of panes (alternates h/v splits automatically):

```toml
[[windows]]
name = "tests"
layout = "tiled"        # tmux layout name

[[windows.panes]]
command = "npm test -- --watch"

[[windows.panes]]
command = "# logs"
```

Common layouts: `even-horizontal`, `even-vertical`, `main-horizontal`, `main-vertical`, `tiled`

### Hooks & Lifecycle

Define actions that run at session lifecycle events:

```toml
[session]
name = "myproject"
on_project_start = "git pull && npm install"  # runs before session created
on_project_exit = "npm run cleanup"            # runs when session killed with -kill flag
```

### pre_window Command

Run a setup command in all panes before their individual commands. Useful for interpreter setup (nvm, rbenv, docker-compose):

```toml
[[windows]]
name = "backend"
pre_window = "nvm use 18"  # runs in each pane before pane-specific commands

  [[windows.splits]]
  type = "h"
  command = "npm start"    # this runs AFTER pre_window

    [[windows.splits.children]]
    type = "v"
    command = "npm test"   # this also runs AFTER pre_window
```

### Startup Window & Pane

Specify which window and pane should be active when the session starts:

```toml
[session]
name = "myproject"
startup_window = "editor"  # can be window name or 1-based index
startup_pane = 0           # 0-indexed pane number within that window
```

## Examples

**Simple nested layout:**
```toml
[[windows]]
name = "dev"

  [[windows.splits]]
  type = "h"            # horizontal: left vs right
  size = 70             # left pane gets 70%, right gets 30%
  command = "nvim"      # run in left pane

    [[windows.splits.children]]
    type = "v"          # nested vertical: top vs bottom
    size = 50
    command = "npm test"

    [[windows.splits.children]]
    type = "v"
    command = "npm run logs"
```

**Equivalent legacy format:**
```toml
[[windows]]
name = "dev"
layout = "tiled"

[[windows.panes]]
command = "nvim"

[[windows.panes]]
command = "npm test"

[[windows.panes]]
command = "npm run logs"
```

## Workflow

1. Create `.tmuxconfig` in your project root
2. Define windows and splits (or legacy panes)
3. Run `tmux-session`
4. Attach with `tmux attach-session -t myproject`

Or add to shell alias:

```bash
alias mksession='tmux-session && tmux attach-session -t'
```

Then use: `mksession myproject` from a directory with `.tmuxconfig`.

## How It Works

1. Parses `.tmuxconfig` (TOML)
2. Kills any existing session with that name
3. Creates new session with first window
4. For each window:
   - If `splits` defined: recursively creates nested splits with specified types/sizes
   - Else if `panes` defined: creates panes with alternating h/v splits, applies layout
   - Runs commands in each pane
5. Auto-attaches to session

## No external deps (besides tmux)

Uses only Go stdlib + `github.com/pelletier/go-toml/v2` for TOML parsing.
