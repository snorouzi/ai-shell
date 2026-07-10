# AI Shell (`ai`)

A local-first AI assistant for your Zsh shell. `ai` is a small Go program that
talks to a locally-running [Ollama](https://ollama.com) instance for inference
and an optional, self-managed [SearXNG](https://searxng.org/) container for web
search. There is no cloud dependency, no paid API, and no telemetry — everything
runs on your machine.

It is used as a shell function:

```bash
ai explain why my docker container is failing
ai model coding
ai models
ai --model qwen2.5-coder:7b "why does this regex not match"
```

The first call lazily starts a background **daemon** that owns the Ollama
connection, the per-session chat memory, and (optionally) the SearXNG container.
Subsequent calls in the same shell reuse that daemon over a Unix socket, so
responses stream back as they are generated and conversation context is kept.

- **Source:** https://github.com/saeedNW/ai-shell

---

## Table of contents

- [AI Shell (`ai`)](#ai-shell-ai)
  - [Table of contents](#table-of-contents)
  - [Features](#features)
  - [Requirements](#requirements)
  - [Install \& build](#install--build)
  - [Shell integration (Zsh)](#shell-integration-zsh)
  - [First run](#first-run)
  - [Commands \& usage](#commands--usage)
    - [Examples](#examples)
  - [Web search (optional)](#web-search-optional)
  - [Command execution (optional)](#command-execution-optional)
  - [Configuration](#configuration)
  - [Project layout](#project-layout)
  - [Upgrading](#upgrading)
    - [Running the daemon via systemd (optional)](#running-the-daemon-via-systemd-optional)
    - [Config schema changes between versions](#config-schema-changes-between-versions)
  - [Troubleshooting](#troubleshooting)
  - [Documentation index](#documentation-index)
  - [License](#license)

---

## Features

- **Local inference only** — chats go to Ollama on `localhost`; nothing leaves
  your machine.
- **Streaming answers** — tokens are printed as they arrive, not buffered.
- **Markdown rendering** — answers are re-rendered as ANSI-styled Markdown in
  your terminal (disabled automatically when output is piped).
- **Terminal context** — the Zsh function captures your current directory, the
  previous command, its exit code, and git status, and feeds them to the model
  so "why did that fail?" just works.
- **Session memory** — a short conversation history is kept per shell session
  (in-memory, capped, not persisted).
- **Model switching** — switch the active model per shell, list configured
  profiles, or override for a single query.
- **Optional web search** — AI Shell manages its own SearXNG container and uses
  it only when the model decides a query needs live information.
- **Optional command execution** — the assistant can suggest shell commands
  with a risk tier and (when you opt in) run them after confirmation, logging
  every executed command.

---

## Requirements

| What                    | Why                                             | Notes                                                            |
| ----------------------- | ----------------------------------------------- | ---------------------------------------------------------------- |
| Arch Linux (or similar) | Install commands shown for Arch; easily adapted | Other distros work with their own package names                  |
| Zsh                     | The integration is a Zsh function               | Bash is _not_ supported                                          |
| Go toolchain            | Build the binary                                | `sudo pacman -S go`                                              |
| Ollama                  | Local inference backend                         | Installed and started by **preflight** on first run, or manually |
| Docker (optional)       | Only needed for web search                      | Skip if `web.enabled: false`                                     |

A GPU is optional — Ollama runs CPU-only too, just slower. The default models
are chosen to fit a ~6 GB VRAM budget.

---

## Install & build

Clone the repository and build from the project root:

```bash
git clone https://github.com/saeedNW/ai-shell.git
cd ai-shell
go build -o ~/go/bin/ai ./cmd/ai
```

This compiles the whole project into a single binary named `ai`. Make sure
`~/go/bin` is on your `PATH`:

```bash
echo 'export PATH="$PATH:$HOME/go/bin"' >> ~/.zshrc
source ~/.zshrc
which ai      # should print ~/go/bin/ai
```

> **Rebuilding later:** any time you change the Go source, re-run the same
> `go build` command. There is no separate "install" step — the binary _is_ the
> whole application.

Dependencies are minimal: the standard library plus `gopkg.in/yaml.v3`.

---

## Shell integration (Zsh)

Copy `ai.zsh` into your dotfiles and source it from `~/.zshrc`:

```bash
mkdir -p ~/.config/zsh
cp /path/to/ai-shell/ai.zsh ~/.config/zsh/ai.zsh
echo 'source ~/.config/zsh/ai.zsh' >> ~/.zshrc
source ~/.zshrc
```

What the function does (see [`ai.zsh`](ai.zsh)):

1. Captures `$?` as the **very first** thing, before anything else changes it.
2. Generates a stable **per-shell session token** (`AI_SHELL_SESSION_ID`) once
   and exports it for the shell's lifetime, so the daemon keeps one conversation
   per terminal window.
3. Captures the previous shell command from history.
4. Passes the captured `cwd` / exit code / last command to the Go binary via
   environment variables and runs `ai "$@"`.

You can also just run the `ai` binary directly without the Zsh wrapper; you
simply lose the automatic context capture (the daemon then falls back to a
`default` session with no shell context).

> **systemd alternative.** Instead of lazy-spawn, you can run the daemon as a
> `--user` service that starts on login (see [Upgrading](#upgrading) for the
> unit file). This is optional and purely a latency/ergonomics choice.

---

## First run

The very first time you run `ai`, it starts the daemon, which runs **preflight
checks** before anything works. This is expected — you have not configured
anything yet.

```bash
ai hello
```

Depending on your system, you may see:

- **Ollama not installed** → preflight detects your GPU and prints the right
  install command (e.g. `sudo pacman -S ollama-cuda && sudo systemctl enable --now ollama`).
- **Ollama installed but not running** → start it:
  `sudo systemctl enable --now ollama`.
- **Models not pulled yet** → it lists the missing models with sizes and asks
  `Pull them now? [Y/n]`. Answer `Y` to download them via Ollama.
- **No config yet** → the **first-run wizard** asks for model tags, whether to
  enable web search, and whether to enable command execution. Sensible defaults
  are offered, so you can mostly press Enter.
- **Web search on** → AI Shell writes its SearXNG config and brings up the
  `ai-shell-searxng` container (requires Docker) — see
  [Web search](#web-search-optional).

A successful run writes your config to `~/.config/ai-shell/config.yaml` and a
`preflight.ok` cache so subsequent starts skip the slow GPU/VRAM detection.

---

## Commands & usage

All commands are sub-commands / argument shapes of the single `ai` binary.

| Command                     | Description                                                                          |
| --------------------------- | ------------------------------------------------------------------------------------ |
| `ai <query>`                | Ask a question; streams the answer back.                                             |
| `ai --model <tag> <query>`  | One-off model override for this query only (does not change the session).            |
| `ai model`                  | Show the current session's model.                                                    |
| `ai model <profile-or-tag>` | Switch the session's model to a profile (`general`, `coding`, …) or a raw model tag. |
| `ai model reset`            | Clear the session override; revert to `ollama.default_profile`.                      |
| `ai models`                 | List configured profiles, their installed status, and the current session model.     |
| `ai daemon`                 | Run the daemon directly (normally started automatically).                            |
| `ai --help` / `ai help`     | Show available commands, options, and usage examples.                                |

### Examples

```bash
# Ask anything
ai how do I check which process is using port 8080
ai explain why my docker container is failing

# Use terminal context automatically — just run a failing command, then ask
docker compose up
ai why did that fail

# Switch models for this shell session
ai model coding            # -> qwen2.5-coder:7b
ai model                   # show current
ai model reset             # revert to config default

# One-off override (session model unchanged)
ai --model qwen2.5-coder:7b "why doesn't this regex match?"

# List profiles
ai models
```

Session state is in-memory only and tied to the shell. Closing the terminal (or
opening a new one) resets to the config default automatically.

---

## Web search (optional)

Web search is off by default and only active when `web.enabled: true` in config.
**AI Shell manages its own SearXNG container** — there is nothing else to
install or run by hand. See the dedicated guide:
[`docs/web-search.md`](docs/web-search.md).

In short, the next daemon start will, when web search is enabled:

1. Check Docker is installed and running (and tell you the install/start command
   if it isn't).
2. Write a known-good `~/.config/ai-shell/searxng/settings.yml` (once; existing
   files are left alone unless `searxng_port` changes).
3. Create and start the `ai-shell-searxng` container, published on
   `searxng_port` (default `8081`).
4. Wait (up to ~30 s) for the instance to become healthy before using it.

If web search can't be set up (no Docker, port taken, container won't start),
AI Shell prints a clear message and simply runs with web search disabled for
that session — everything else keeps working. When the model decides a query
needs current information, it replies with a `{"web_search": "..."}` tool call;
the daemon runs the search and folds the results into the final answer.

---

## Command execution (optional)

Off by default (`execution.enabled: false`). AI Shell only explains and suggests
— it never runs anything unless you turn it on. To enable, edit
`~/.config/ai-shell/config.yaml`:

```yaml
execution:
  enabled: true
```

When enabled, the assistant returns a **structured suggestion**
(`{"explanation", "commands", "risk"}`) instead of prose. Each suggested command
is classified against the `execution.risk_rules` globs in config to assign a
risk tier (`low` / `medium` / `high`). You are prompted before anything runs:

```plaintext
Suggested command:
  sudo pacman -S docker

Risk: medium
Run it? [y/N]
```

- Low/medium risk → confirm with `y`.
- High risk (e.g. `sudo *`, `rm -rf*`, `dd *`) → you must type the literal
  word `yes` (configurable via `require_typed_confirmation_for_high_risk`).

Every executed command is logged to `~/.local/state/ai-shell/executed_commands.log`.

---

## Configuration

The config lives at `~/.config/ai-shell/config.yaml`. It is created by the
first-run wizard, or you can write it yourself. See
[`docs/configuration.md`](docs/configuration.md) for the full schema and every
field, including the model profiles, context toggles, execution risk rules, and
web settings.

Minimal example:

```yaml
app:
  name: ai-shell
  version: 0.1.0

ollama:
  url: http://localhost:11434
  models:
    general: qwen3:8b
    coding: qwen2.5-coder:7b
    fast: qwen3:4b
  default_profile: general
  context_length: 4096
  request_timeout_seconds: 60

daemon:
  socket_path: '${XDG_RUNTIME_DIR}/ai-shell.sock'
  idle_timeout_minutes: 30
  log_file: '~/.local/state/ai-shell/daemon.log'

session:
  max_history_messages: 20

context:
  include_last_command: true
  include_exit_code: true
  include_cwd: true
  include_git_status: true

execution:
  enabled: false
  confirm_before_execute: true
  require_typed_confirmation_for_high_risk: true
  risk_rules:
    high: ['rm -rf*', 'sudo *', '* /etc/*', 'dd *', 'mkfs*']
    medium: ['pacman -R*', 'systemctl stop*', 'git push --force*']
    low: []
  log_file: '~/.local/state/ai-shell/executed_commands.log'

web:
  enabled: false
  provider: searxng
  searxng_port: 8081

ui:
  render_markdown: true
```

`~` and `${XDG_RUNTIME_DIR}`-style variables are expanded when paths are loaded.

---

## Project layout

```plaintext
ai-shell/
├── cmd/ai/main.go          # entrypoint — dual mode: daemon or client
├── ai.zsh                  # Zsh shell function (source from ~/.zshrc)
├── docs/                   # additional documentation (this repo)
├── internal/
│   ├── config/             # load/save YAML config + defaults
│   ├── preflight/          # Ollama/GPU checks, model pulling, first-run wizard
│   ├── ollama/             # HTTP client for Ollama's API (+ `ollama pull`)
│   ├── daemon/             # Unix socket server, request routing, web tool loop
│   ├── session/            # in-memory per-session chat state
│   ├── context/            # gather cwd/git/last-command/exit-code
│   ├── execution/          # command running + risk classification
│   ├── web/                # SearXNG HTTP client (query an instance)
│   ├── searxng/            # manages the SearXNG *container* lifecycle
│   └── markdown/           # tiny dependency-free ANSI Markdown renderer
└── go.mod
```

A deeper walkthrough of each package is in
[`docs/architecture.md`](docs/architecture.md).

> **Why two web packages?** `internal/web` is a _pure_ HTTP client that queries an
> already-running SearXNG instance. `internal/searxng` owns the _container_ —
> starting it, writing its config, health-checking it. Keeping them separate
> means `internal/web` stays simple and `internal/searxng` is the only place that
> shells out to Docker.

---

## Upgrading

Because the binary _is_ the application, upgrading is just: rebuild, then either
let the lazy-spawn restart the daemon or restart it explicitly.

```bash
cd /path/to/ai-shell
git pull                 # if tracking a repo
go build -o ~/go/bin/ai ./cmd/ai
```

Then restart the daemon so it picks up the new binary and any config-schema
changes:

```bash
# If using lazy-spawn:
pkill -f "ai daemon"
# Next `ai` call starts the new daemon automatically.

# If using the systemd user unit (see below):
systemctl --user restart ai-shell
```

### Running the daemon via systemd (optional)

Instead of lazy-spawn, start the daemon on login:

```bash
mkdir -p ~/.config/systemd/user
cat > ~/.config/systemd/user/ai-shell.service << 'EOF'
[Unit]
Description=AI Shell daemon

[Service]
ExecStart=%h/go/bin/ai daemon
Restart=on-failure

[Install]
WantedBy=default.target
EOF

systemctl --user enable --now ai-shell.service
```

### Config schema changes between versions

If you are upgrading from an older config that used `web.searxng_url`, note that
it has been **replaced by `web.searxng_port`** (just an integer). AI Shell now
owns the full URL internally (`http://localhost:<port>`). On load, an old
`searxng_url` field is simply ignored and `searxng_port` defaults to `8081`.
Update your config to the new `web` block (see
[`docs/configuration.md`](docs/configuration.md)).

---

## Troubleshooting

| Symptom                          | Likely cause / fix                                                                                                                              |
| -------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| `command not found: ai`          | `~/go/bin` not on `PATH`, or you haven't rebuilt/sourced `.zshrc`.                                                                              |
| `ai` hangs on first call         | Daemon starting + running preflight for the first time — give it a few seconds, especially while pulling models.                                |
| "daemon unreachable"             | Ollama may be down — `systemctl status ollama`.                                                                                                 |
| Responses very slow              | Model too large for your VRAM, spilling into RAM — check `ai models` and pick a smaller profile, or lower `ollama.context_length`.              |
| Web search not working           | Docker missing/stopped, port taken, or container won't start — see [`docs/web-search.md`](docs/web-search.md). AI Shell still works without it. |
| Config changes not taking effect | Some settings are read at daemon startup — `pkill -f "ai daemon"` (or `systemctl --user restart ai-shell`) and let it restart.                  |
| Want to fully reset              | Delete `~/.config/ai-shell/config.yaml` and `~/.local/state/ai-shell/preflight.ok`, then run `ai hello` to redo setup.                          |

Logs:

- Daemon: `~/.local/state/ai-shell/daemon.log`
- Executed commands (execution on): `~/.local/state/ai-shell/executed_commands.log`
- SearXNG config: `~/.config/ai-shell/searxng/settings.yml`

---

## Documentation index

- [`docs/configuration.md`](docs/configuration.md) — full config file reference.
- [`docs/web-search.md`](docs/web-search.md) — SearXNG container management,
  Docker requirements, port conflicts, and troubleshooting.
- [`docs/architecture.md`](docs/architecture.md) — internal package design and
  request/response flow.

---

## License

MIT — see [LICENSE](LICENSE).
