# Bash completion for wyrm.
#
# Install: source this file (e.g. from ~/.bashrc), or copy it into your
# bash-completion directory (/usr/local/etc/bash_completion.d/ on macOS with
# Homebrew's bash-completion, /etc/bash_completion.d/ on most Linux distros).
# `brew install jskoll/tap/wyrm` installs it automatically.
#
# Dynamic completions shell out to wyrm itself, reusing its own config
# discovery and tmux session listing rather than reimplementing them here:
#   -config  -> `wyrm -list-configs`       (local + shared config file paths)
#   -format  -> static: table json toml names
#   bare arg -> `wyrm -list -format names` (running session names, for
#                `wyrm <name>` — see the wyrm(1) usage for what that does)

_wyrm_complete() {
    local cur prev
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    case "$prev" in
        -config)
            COMPREPLY=($(compgen -W "$(wyrm -list-configs 2>/dev/null)" -- "$cur"))
            return
            ;;
        -format)
            COMPREPLY=($(compgen -W "table json toml names" -- "$cur"))
            return
            ;;
    esac

    if [[ "$cur" == -* ]]; then
        COMPREPLY=($(compgen -W "-config -kill -pick -save -version -migrate-config -validate -list -format -edit -list-configs" -- "$cur"))
        return
    fi

    COMPREPLY=($(compgen -W "$(wyrm -list -format names 2>/dev/null)" -- "$cur"))
}

complete -F _wyrm_complete wyrm
