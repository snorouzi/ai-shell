# AI Shell — Zsh integration
#
# Source this file from your ~/.zshrc (or drop it into a plugin directory):
#     source /path/to/ai.zsh
#
# Requires the `ai` binary to be on your PATH, e.g.:
#     go build -o ~/go/bin/ai ./cmd/ai

ai() {
  # Capture $? as the very first thing — any command after this would reset it.
  local last_exit=$?

  # Stable per-shell session token: generate once, then reuse for the shell's life.
  if [[ -z "${AI_SHELL_SESSION_ID:-}" ]]; then
    AI_SHELL_SESSION_ID="$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n')"
    export AI_SHELL_SESSION_ID
  fi

  # Previous command from history (the one whose exit code we just captured).
  local last_cmd="${history[$((HISTCMD - 1))]:-}"

  # Hand the captured context to the Go client via env vars. It connects to the
  # daemon (spawning it if needed) and streams the response as it arrives.
  AI_SHELL_EXIT_CODE="$last_exit" \
  AI_SHELL_LAST_COMMAND="$last_cmd" \
    command ai "$@"
}
