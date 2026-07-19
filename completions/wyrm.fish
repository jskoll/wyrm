# Fish completion for wyrm.
#
# Install: copy to ~/.config/fish/completions/wyrm.fish (fish auto-loads
# anything there). `brew install jskoll/tap/wyrm` installs it automatically.
#
# Dynamic completions shell out to wyrm itself, reusing its own config
# discovery and tmux session listing rather than reimplementing them here:
#   -config  -> `wyrm -list-configs`       (local + shared config file paths)
#   -format  -> static: table json toml names
#   bare arg -> `wyrm -list -format names` (running session names, for
#                `wyrm <name>` — see the wyrm(1) usage for what that does)
#
# wyrm's flags are single-dash (Go's flag package convention, e.g.
# "-config", not "--config"), hence "-o" (old-style option) below rather
# than fish's usual "-l" (GNU-style long option).

complete -c wyrm -o config -d 'config file path' -r -a '(wyrm -list-configs 2>/dev/null)'
complete -c wyrm -o kill -d 'kill the session (runs on_project_exit)'
complete -c wyrm -o pick -d 'fuzzy-pick a running session to attach to'
complete -c wyrm -o save -d 'save the running session layout as a config for this folder'
complete -c wyrm -o version -d 'print version and exit'
complete -c wyrm -o migrate-config -d 'move the local config into the shared config directory'
complete -c wyrm -o validate -d 'check the effective config without building a session'
complete -c wyrm -o list -d 'list running tmux sessions non-interactively'
complete -c wyrm -o format -d 'output format for -list' -x -a 'table json toml names'
complete -c wyrm -o edit -d 'open the resolved config in $EDITOR'
complete -c wyrm -o list-configs -d 'list candidate config file paths'
complete -c wyrm -n 'test (count (commandline -opc)) -eq 1' -x -a '(wyrm -list -format names 2>/dev/null)' -d 'running session'
