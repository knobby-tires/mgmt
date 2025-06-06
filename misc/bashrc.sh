#!/usr/bin/env bash

_cli_bash_autocomplete_mgmt() {
	local cur prev opts base
	COMPREPLY=()
	cur="${COMP_WORDS[COMP_CWORD]}"
	prev="${COMP_WORDS[COMP_CWORD-1]}"
	opts=$( ${COMP_WORDS[@]:0:$COMP_CWORD} --generate-bash-completion )
	COMPREPLY=( $(compgen -W "${opts}" -- ${cur}) )
	return 0
}

complete -F _cli_bash_autocomplete_mgmt mgmt
