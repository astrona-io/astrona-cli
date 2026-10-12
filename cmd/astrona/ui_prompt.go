package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func uiShellPrompt(noColor bool) string {
	if noColor {
		return "astrona-cli > "
	}
	return "\x1b[38;5;117mastrona-cli >\x1b[97m "
}

// Temporary startup files keep the user's shell setup and replace its prompt.
// They belong to one terminal and never change the user's configuration files.
func configureUITerminalPrompt(cmd *exec.Cmd) (func(), error) {
	noop := func() {}
	env := cmd.Environ()
	value := func(key string) string {
		for i := len(env) - 1; i >= 0; i-- {
			if v, ok := strings.CutPrefix(env[i], key+"="); ok {
				return v
			}
		}
		return ""
	}
	prompt := uiShellPrompt(value("NO_COLOR") != "")
	cmd.Env = append(env, "PS1="+prompt)
	shell := filepath.Base(cmd.Path)
	if shell == "fish" {
		cmd.Args = append(cmd.Args, "--interactive", "--init-command", "function fish_prompt; printf '"+prompt+"'; end; function fish_right_prompt; end")
		return noop, nil
	}
	if shell != "zsh" && shell != "bash" && shell != "sh" && shell != "dash" && shell != "ksh" {
		return noop, nil
	}
	dir, err := os.MkdirTemp("", "astrona-ui-shell-")
	if err != nil {
		return noop, fmt.Errorf("prepare terminal prompt: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	files := map[string]string{}
	switch shell {
	case "zsh":
		original := value("ZDOTDIR")
		if original == "" {
			original = value("HOME")
		}
		cmd.Env = append(cmd.Env, "ASTRONA_UI_SHELL_DIR="+dir, "ASTRONA_UI_SHELL_RC_DIR="+original, "ZDOTDIR="+dir)
		files[".zshenv"] = `ZDOTDIR=$ASTRONA_UI_SHELL_RC_DIR
[[ -r "$ZDOTDIR/.zshenv" ]] && source "$ZDOTDIR/.zshenv"
ASTRONA_UI_SHELL_RC_DIR=${ZDOTDIR:-$HOME}
ZDOTDIR=$ASTRONA_UI_SHELL_DIR
`
		files[".zshrc"] = `ZDOTDIR=$ASTRONA_UI_SHELL_RC_DIR
unset ASTRONA_UI_SHELL_DIR ASTRONA_UI_SHELL_RC_DIR
[[ -r "$ZDOTDIR/.zshrc" ]] && source "$ZDOTDIR/.zshrc"
function __astrona_ui_prompt() {
  local previous=$?
  PROMPT='astrona-cli > '
  RPROMPT=''
  return $previous
}
precmd_functions+=(__astrona_ui_prompt)
__astrona_ui_prompt
`
		cmd.Args = append(cmd.Args, "-i")
	case "bash":
		files["rc"] = `[[ -r "$HOME/.bashrc" ]] && source "$HOME/.bashrc"
__astrona_ui_prompt() {
  local previous=$?
  PS1='astrona-cli > '
  return "$previous"
}
__astrona_ui_hooks=("${PROMPT_COMMAND[@]}")
unset PROMPT_COMMAND
printf -v PROMPT_COMMAND '%s\n' "${__astrona_ui_hooks[@]}" '__astrona_ui_prompt'
unset __astrona_ui_hooks
__astrona_ui_prompt
`
		cmd.Args = append(cmd.Args, "--rcfile", filepath.Join(dir, "rc"), "-i")
	default:
		cmd.Env = append(cmd.Env, "ASTRONA_UI_SHELL_RC="+value("ENV"), "ENV="+filepath.Join(dir, "rc"))
		files["rc"] = `[ -n "$ASTRONA_UI_SHELL_RC" ] && [ -r "$ASTRONA_UI_SHELL_RC" ] && . "$ASTRONA_UI_SHELL_RC"
ENV=$ASTRONA_UI_SHELL_RC
unset ASTRONA_UI_SHELL_RC
PS1='astrona-cli > '
`
		cmd.Args = append(cmd.Args, "-i")
	}
	for name, body := range files {
		replacement := prompt
		if value("NO_COLOR") == "" {
			switch shell {
			case "bash":
				replacement = `\[\e[38;5;117m\]astrona-cli >\[\e[97m\] `
			case "zsh":
				replacement = "%F{117}astrona-cli >%F{15} "
			}
		}
		body = strings.ReplaceAll(body, "astrona-cli > ", replacement)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			cleanup()
			return noop, fmt.Errorf("prepare terminal prompt: %w", err)
		}
	}
	return cleanup, nil
}
