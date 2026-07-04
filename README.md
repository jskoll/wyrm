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

`.tmuxconfig` is TOML with three main sections:

```toml
[session]
name = "myproject"      # session name (defaults to directory name)
root = "."              # working directory (expanded via $HOME, etc.)

[[windows]]
name = "editor"         # window name
layout = "even-horizontal"  # tmux layout (tiled, main-horizontal, etc.)

[[windows.panes]]
command = "nvim"        # command to run in first pane

[[windows.panes]]
command = "# sidebar"   # second pane (commands are optional)

[[windows]]
name = "tests"
layout = "tiled"

[[windows.panes]]
command = "npm test -- --watch"

[[windows.panes]]
command = "# logs"
```

### Layouts

Common tmux layouts:
- `even-horizontal` — split horizontally, equal width
- `even-vertical` — split vertically, equal height
- `main-horizontal` — large pane on top, rest below
- `main-vertical` — large pane on left, rest on right
- `tiled` — balanced grid
- (omit to auto-tile if multiple panes)

## Workflow

1. Create `.tmuxconfig` in your project root
2. Define windows and panes
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
4. Splits panes in each window (alternating h/v)
5. Runs commands in each pane
6. Applies layout
7. Prints attach command

## No external deps (besides tmux)

Uses only Go stdlib + `github.com/pelletier/go-toml/v2` for TOML parsing.
