# Bash completion for wyrm.
#
# Install: source this file (e.g. from ~/.bashrc), or copy it into your
# bash-completion directory (/usr/local/etc/bash_completion.d/ on macOS with
# Homebrew's bash-completion, /etc/bash_completion.d/ on most Linux distros).
# `brew install jskoll/tap/wyrm` installs it automatically.
#
# wyrm uses git-style subcommands (`wyrm kill`, `wyrm list`, ...). Dynamic
# completions shell out to wyrm itself, reusing its own config discovery and
# tmux session listing rather than reimplementing them here:
#   -config      -> `wyrm list-configs`      (local + shared config file paths)
#   -format      -> static: table json toml names
#   first token  -> subcommands + `wyrm list -format names` (running session
#                   names, since a bare `wyrm <name>` attaches by name)

_wyrm_complete() {
    local cur prev cmd
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"
    cmd="${COMP_WORDS[1]}"

    # Flag-value completions, wherever the flag appears.
    case "$prev" in
        -config)
            COMPREPLY=($(compgen -W "$(wyrm list-configs 2>/dev/null)" -- "$cur"))
            return
            ;;
        -format)
            COMPREPLY=($(compgen -W "table json toml names" -- "$cur"))
            return
            ;;
        -template|-t)
            COMPREPLY=($(compgen -W "node python go rust monorepo minimal" -- "$cur"))
            return
            ;;
    esac

    local subcommands="up restart kill pick tui save edit validate status list list-configs migrate-config clone selfupdate version help init"

    # First token: a subcommand, or a running session name to attach to.
    if [[ "$COMP_CWORD" -eq 1 ]]; then
        if [[ "$cur" == -* ]]; then
            COMPREPLY=($(compgen -W "-config -h -help -version" -- "$cur"))
        else
            COMPREPLY=($(compgen -W "$subcommands $(wyrm list -format names 2>/dev/null)" -- "$cur"))
        fi
        return
    fi

    # Subcommand-specific flags.
    if [[ "$cur" == -* ]]; then
        case "$cmd" in
            up|edit|validate) COMPREPLY=($(compgen -W "-config" -- "$cur")) ;;
            restart) COMPREPLY=($(compgen -W "-config -n -d -all -a -yes -y -var" -- "$cur")) ;;
            kill) COMPREPLY=($(compgen -W "-config -n -all -a -yes -y" -- "$cur")) ;;
            save) COMPREPLY=($(compgen -W "-config -stdout -n -dry-run -o" -- "$cur")) ;;
            init) COMPREPLY=($(compgen -W "-config -template -t -force -f" -- "$cur")) ;;
            status) COMPREPLY=($(compgen -W "-format -session -v" -- "$cur")) ;;
            list) COMPREPLY=($(compgen -W "-format" -- "$cur")) ;;
            selfupdate) COMPREPLY=($(compgen -W "-check -version" -- "$cur")) ;;
            *) COMPREPLY=() ;;
        esac
        return
    fi
}

complete -F _wyrm_complete wyrm
