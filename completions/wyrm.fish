# Fish completion for wyrm.
#
# Install: copy to ~/.config/fish/completions/wyrm.fish (fish auto-loads
# anything there). `brew install jskoll/tap/wyrm` installs it automatically.
#
# wyrm uses git-style subcommands (`wyrm kill`, `wyrm list`, ...). Dynamic
# completions shell out to wyrm itself, reusing its own config discovery and
# tmux session listing rather than reimplementing them here:
#   -config      -> `wyrm list-configs`      (local + shared config file paths)
#   -format      -> static: table json toml names
#   first token  -> subcommands + `wyrm list -format names` (running session
#                   names, since a bare `wyrm <name>` attaches by name)
#
# wyrm's flags are single-dash (Go's flag package convention, e.g. "-config",
# not "--config"), hence "-o" (old-style option) below rather than fish's
# usual "-l" (GNU-style long option).

set -l subcommands up restart kill pick tui save edit validate status list list-configs migrate-config clone selfupdate version help init

# Don't fall back to filename completion.
complete -c wyrm -f

# First token: subcommands...
complete -c wyrm -n "not __fish_seen_subcommand_from $subcommands" -a up -d 'build or attach the current folder'\''s session'
complete -c wyrm -n "not __fish_seen_subcommand_from $subcommands" -a restart -d 'stop the session and build it again'
complete -c wyrm -n "not __fish_seen_subcommand_from $subcommands" -a kill -d 'destroy the session (runs on_project_exit)'
complete -c wyrm -n "not __fish_seen_subcommand_from $subcommands" -a pick -d 'fuzzy-pick a running session to attach to'
complete -c wyrm -n "not __fish_seen_subcommand_from $subcommands" -a tui -d 'full-screen session-management TUI'
complete -c wyrm -n "not __fish_seen_subcommand_from $subcommands" -a save -d 'save the running session layout as a config'
complete -c wyrm -n "not __fish_seen_subcommand_from $subcommands" -a edit -d 'open the resolved config in $EDITOR'
complete -c wyrm -n "not __fish_seen_subcommand_from $subcommands" -a validate -d 'check the effective config'
complete -c wyrm -n "not __fish_seen_subcommand_from $subcommands" -a status -d 'print agent status across sessions'
complete -c wyrm -n "not __fish_seen_subcommand_from $subcommands" -a list -d 'list running tmux sessions'
complete -c wyrm -n "not __fish_seen_subcommand_from $subcommands" -a list-configs -d 'list candidate config file paths'
complete -c wyrm -n "not __fish_seen_subcommand_from $subcommands" -a migrate-config -d 'move the local config into the shared dir'
complete -c wyrm -n "not __fish_seen_subcommand_from $subcommands" -a clone -d 'git clone, then build and attach a session'
complete -c wyrm -n "not __fish_seen_subcommand_from $subcommands" -a init -d 'scaffold a new config interactively or from a template'
complete -c wyrm -n "not __fish_seen_subcommand_from $subcommands" -a selfupdate -d 'download and install the latest release'
complete -c wyrm -n "not __fish_seen_subcommand_from $subcommands" -a version -d 'print version and exit'
complete -c wyrm -n "not __fish_seen_subcommand_from $subcommands" -a help -d 'show help'
# ...and running session names (bare `wyrm <name>` attaches by name).
complete -c wyrm -n "not __fish_seen_subcommand_from $subcommands" -a '(wyrm list -format names 2>/dev/null)' -d 'running session'

# Subcommand flags.
complete -c wyrm -n '__fish_seen_subcommand_from up restart kill edit validate' -o config -d 'config file path' -r -a '(wyrm list-configs 2>/dev/null)'
complete -c wyrm -n '__fish_seen_subcommand_from restart kill' -o all -d 'apply to all active sessions'
complete -c wyrm -n '__fish_seen_subcommand_from restart kill' -o a -d 'apply to all active sessions'
complete -c wyrm -n '__fish_seen_subcommand_from restart kill' -o yes -d 'skip confirmation prompt'
complete -c wyrm -n '__fish_seen_subcommand_from restart kill' -o y -d 'skip confirmation prompt'
complete -c wyrm -n '__fish_seen_subcommand_from save' -o config -d 'config file path' -r -a '(wyrm list-configs 2>/dev/null)'
complete -c wyrm -n '__fish_seen_subcommand_from save' -o o -d 'config file path or - for stdout' -r
complete -c wyrm -n '__fish_seen_subcommand_from save' -o stdout -d 'print config to stdout'
complete -c wyrm -n '__fish_seen_subcommand_from save' -o n -d 'dry run preview'
complete -c wyrm -n '__fish_seen_subcommand_from save' -o dry-run -d 'dry run preview'
complete -c wyrm -n '__fish_seen_subcommand_from init' -o config -d 'config file path' -r -a '(wyrm list-configs 2>/dev/null)'
complete -c wyrm -n '__fish_seen_subcommand_from init' -o template -d 'starter template' -x -a 'node python go rust monorepo minimal'
complete -c wyrm -n '__fish_seen_subcommand_from init' -o t -d 'starter template' -x -a 'node python go rust monorepo minimal'
complete -c wyrm -n '__fish_seen_subcommand_from init' -o force -d 'overwrite existing config without prompting'
complete -c wyrm -n '__fish_seen_subcommand_from init' -o f -d 'overwrite existing config without prompting'
complete -c wyrm -n '__fish_seen_subcommand_from status' -o format -d 'output format' -x -a 'text json tmux waybar sketchybar'
complete -c wyrm -n '__fish_seen_subcommand_from status' -o session -d 'filter to session' -x
complete -c wyrm -n '__fish_seen_subcommand_from status' -o v -d 'verbose output'
complete -c wyrm -n '__fish_seen_subcommand_from list' -o format -d 'output format' -x -a 'table json toml names'
complete -c wyrm -n '__fish_seen_subcommand_from selfupdate' -o check -d 'report an available update without installing it'
complete -c wyrm -n '__fish_seen_subcommand_from selfupdate' -o version -d 'install this version instead of the latest' -x
