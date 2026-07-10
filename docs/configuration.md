# Configuration reference

AI Shell reads its configuration from `~/.config/ai-shell/config.yaml`. The file
is created by the first-run wizard, or you can write it by hand. This document
describes every field, with the shipped defaults.

`~` and `${XDG_RUNTIME_DIR}`-style variables are expanded when paths are loaded.

---

## Top-level structure

```yaml
app: { name, version }
ollama: { url, models, default_profile, context_length, request_timeout_seconds }
daemon: { socket_path, idle_timeout_minutes, log_file }
session: { max_history_messages }
context: { include_last_command, include_exit_code, include_cwd, include_git_status }
execution:
  {
    enabled,
    confirm_before_execute,
    require_typed_confirmation_for_high_risk,
    risk_rules,
    log_file,
  }
web: { enabled, provider, searxng_port }
ui: { render_markdown }
```

---

## `app`

| Field     | Type   | Default    | Notes          |
| --------- | ------ | ---------- | -------------- |
| `name`    | string | `ai-shell` | Informational. |
| `version` | string | `0.1.0`    | Informational. |

---

## `ollama`

Connection and model settings for the local inference backend.

| Field                     | Type   | Default                                                           | Notes                                                                                        |
| ------------------------- | ------ | ----------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| `url`                     | string | `http://localhost:11434`                                          | Ollama's HTTP API address.                                                                   |
| `models`                  | map    | `general: qwen3:8b`, `coding: qwen2.5-coder:7b`, `fast: qwen3:4b` | Keyed by **profile name**; value is an Ollama model tag. Extensible — add your own profiles. |
| `default_profile`         | string | `general`                                                         | Which profile `ai model reset` and new sessions fall back to.                                |
| `context_length`          | int    | `4096`                                                            | Sent to Ollama as `num_ctx`. Raise it if you have more VRAM.                                 |
| `request_timeout_seconds` | int    | `60`                                                              | Per-request HTTP timeout to Ollama.                                                          |

`models` is a free-form map, not a fixed enum — add profiles like
`big: qwen3:14b` and they immediately become available to `ai model big`.

---

## `daemon`

| Field                  | Type   | Default                              | Notes                                                    |
| ---------------------- | ------ | ------------------------------------ | -------------------------------------------------------- |
| `socket_path`          | string | `${XDG_RUNTIME_DIR}/ai-shell.sock`   | Unix socket the client/daemon communicate over.          |
| `idle_timeout_minutes` | int    | `30`                                 | Daemon may exit after this long with no active sessions. |
| `log_file`             | string | `~/.local/state/ai-shell/daemon.log` | Daemon log destination.                                  |

---

## `session`

| Field                  | Type | Default | Notes                                                                          |
| ---------------------- | ---- | ------- | ------------------------------------------------------------------------------ |
| `max_history_messages` | int  | `20`    | Per-session chat history kept in memory. Old messages are dropped beyond this. |

Session state is **in-memory only**, tied to a per-shell token, and discarded
when the daemon evicts the session or is restarted. There is no persistence.

---

## `context`

What the Zsh function captures and feeds to the model on every query.

| Field                  | Type | Default | Notes                                                 |
| ---------------------- | ---- | ------- | ----------------------------------------------------- |
| `include_last_command` | bool | `true`  | Previous shell command (from history).                |
| `include_exit_code`    | bool | `true`  | Exit code of the previous command (`$?`).             |
| `include_cwd`          | bool | `true`  | Current working directory.                            |
| `include_git_status`   | bool | `true`  | Git branch + `status --porcelain` when inside a repo. |

These are injected as a system-message-style prefix, not concatenated into your
prompt.

---

## `execution`

Controls command suggestion and execution. **Off by default.**

| Field                                      | Type   | Default                                         | Notes                                                            |
| ------------------------------------------ | ------ | ----------------------------------------------- | ---------------------------------------------------------------- |
| `enabled`                                  | bool   | `false`                                         | When `false`, AI Shell only shows suggestions — never runs them. |
| `confirm_before_execute`                   | bool   | `true`                                          | Require confirmation before running suggested commands.          |
| `require_typed_confirmation_for_high_risk` | bool   | `true`                                          | High-risk commands need the literal word `yes`.                  |
| `risk_rules`                               | object | see below                                       | Glob patterns per tier (config-driven, not hardcoded).           |
| `log_file`                                 | string | `~/.local/state/ai-shell/executed_commands.log` | Audit log of executed commands.                                  |

### `risk_rules`

Shell-style globs (where `*` matches any characters, including spaces and
slashes). The highest matching tier, combined with the model's own claimed
risk, determines the final tier.

```yaml
risk_rules:
  high:
    - 'rm -rf*'
    - 'sudo *'
    - '* /etc/*'
    - 'dd *'
    - 'mkfs*'
  medium:
    - 'pacman -R*'
    - 'systemctl stop*'
    - 'git push --force*'
  low: []
```

- Low/medium risk → confirm with `y` / `yes`.
- High risk → type `yes` when `require_typed_confirmation_for_high_risk` is on.

Every executed command (with timestamp, risk tier, and exit code) is appended to
`log_file`.

---

## `web`

Settings for the optional SearXNG-backed web search.

| Field          | Type   | Default   | Notes                                                                                          |
| -------------- | ------ | --------- | ---------------------------------------------------------------------------------------------- |
| `enabled`      | bool   | `false`   | Master switch. When `false`, **no Docker check, no container management** happens at all.      |
| `provider`     | string | `searxng` | Only SearXNG is supported.                                                                     |
| `searxng_port` | int    | `8081`    | Host port AI Shell publishes the container on. The container's internal port is always `8080`. |

> **Changed from earlier versions:** `web.searxng_url` was replaced by
> `web.searxng_port`. AI Shell now owns the full URL
> (`http://localhost:<searxng_port>`) because it is the one starting the
> container. See [`../docs/web-search.md`](../docs/web-search.md).

When `enabled: true`, the daemon's preflight:

1. Verifies Docker is installed and running.
2. Writes a known-good `~/.config/ai-shell/searxng/settings.yml` (once; not
   overwritten unless `searxng_port` changes).
3. Creates/starts the `ai-shell-searxng` container on `searxng_port`.
4. Health-checks it (up to ~30 s) before declaring web search ready.

If any step fails, web search is disabled for that session and everything else
keeps working.

---

## `ui`

| Field             | Type | Default | Notes                                                                                         |
| ----------------- | ---- | ------- | --------------------------------------------------------------------------------------------- |
| `render_markdown` | bool | `true` | Render answers as ANSI-styled Markdown in a TTY. Automatically disabled when stdout is piped. |

---

Project source: https://github.com/saeedNW/ai-shell
