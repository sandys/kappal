# Bash completion for kappal
#
# Installation:
#   source /path/to/kappal-completion.bash
#
# Or add to ~/.bashrc:
#   source /path/to/kappal/scripts/kappal-completion.bash

_kappal_completions() {
    local cur prev words cword
    _init_completion || return

    local commands="up down ps logs exec build clean"
    local global_opts="-f --file -p --project-name -h --help"

    case "$prev" in
        -f|--file)
            # Complete docker-compose files
            _filedir '@(yaml|yml)'
            return
            ;;
        -p|--project-name)
            # No completion for project name
            return
            ;;
        -o|--format)
            COMPREPLY=($(compgen -W "table json yaml" -- "$cur"))
            return
            ;;
    esac

    case "${words[1]}" in
        up)
            COMPREPLY=($(compgen -W "--build -d --detach $global_opts" -- "$cur"))
            ;;
        down)
            COMPREPLY=($(compgen -W "-v --volumes $global_opts" -- "$cur"))
            ;;
        ps)
            COMPREPLY=($(compgen -W "-a --all -o --format $global_opts" -- "$cur"))
            ;;
        logs)
            # Complete with service names from docker-compose file
            if [[ -f "docker-compose.yaml" ]]; then
                local services=$(grep -E '^\s+\w+:$' docker-compose.yaml 2>/dev/null | sed 's/://g' | tr -d ' ')
                COMPREPLY=($(compgen -W "$services -f --follow --tail $global_opts" -- "$cur"))
            elif [[ -f "docker-compose.yml" ]]; then
                local services=$(grep -E '^\s+\w+:$' docker-compose.yml 2>/dev/null | sed 's/://g' | tr -d ' ')
                COMPREPLY=($(compgen -W "$services -f --follow --tail $global_opts" -- "$cur"))
            else
                COMPREPLY=($(compgen -W "-f --follow --tail $global_opts" -- "$cur"))
            fi
            ;;
        exec)
            # Complete with service names
            if [[ -f "docker-compose.yaml" ]]; then
                local services=$(grep -E '^\s+\w+:$' docker-compose.yaml 2>/dev/null | sed 's/://g' | tr -d ' ')
                COMPREPLY=($(compgen -W "$services -i --interactive -t --tty $global_opts" -- "$cur"))
            elif [[ -f "docker-compose.yml" ]]; then
                local services=$(grep -E '^\s+\w+:$' docker-compose.yml 2>/dev/null | sed 's/://g' | tr -d ' ')
                COMPREPLY=($(compgen -W "$services -i --interactive -t --tty $global_opts" -- "$cur"))
            else
                COMPREPLY=($(compgen -W "-i --interactive -t --tty $global_opts" -- "$cur"))
            fi
            ;;
        build)
            COMPREPLY=($(compgen -W "$global_opts" -- "$cur"))
            ;;
        clean)
            COMPREPLY=($(compgen -W "$global_opts" -- "$cur"))
            ;;
        *)
            if [[ "$cur" == -* ]]; then
                COMPREPLY=($(compgen -W "$global_opts" -- "$cur"))
            else
                COMPREPLY=($(compgen -W "$commands" -- "$cur"))
            fi
            ;;
    esac
}

complete -F _kappal_completions kappal
