# Architecture

AI Shell is a single Go binary (`ai`) with two run modes that talk over a Unix
domain socket. This document walks the internal packages and the request flow.
It is meant to help a Go beginner (the project's target maintainer) navigate the
codebase.

Module: `ai-shell`. Go 1.26+, dependencies: **standard library + `gopkg.in/yaml.v3`**
only. No CLI framework, no router, no ORM, no UUID library — the surface area
doesn't need them.

---

## Modes & entrypoint

`cmd/ai/main.go` parses arguments and decides the mode:

- `ai daemon` → runs `daemon.Run`, which performs preflight and then serves the
  socket.
- Everything else → **client mode**: ensures the daemon is running (spawns it in
  the background if the socket isn't alive), dials the Unix socket, and sends a
  JSON request, streaming responses back.

The Zsh function (`ai.zsh`) calls the binary as the client, injecting context
via environment variables.

---

## Package map

```plaintext
cmd/ai/main.go          client + daemon entrypoint, arg parsing, TTY/Mardown glue
internal/config         YAML config load/save/defaults (~/.config/ai-shell/config.yaml)
internal/preflight      Ollama/GPU checks, model pulling, first-run wizard, web setup
internal/ollama         HTTP client for Ollama's /api/generate (streaming) + /api/tags, `ollama pull`
internal/daemon         Unix socket server, JSON protocol, request routing, web tool loop
internal/session        in-memory per-session chat state + pending suggestion
internal/context        gather cwd/git/last-command/exit-code into a system prompt
internal/execution      structured suggestions, risk classification, confirmation, execution, audit log
internal/web            pure SearXNG HTTP client (query a running instance)
internal/searxng        manages the SearXNG *container* lifecycle (Docker via os/exec)
internal/markdown       tiny dependency-free ANSI Markdown renderer
```

---

## Startup flow

```plaintext
ai <query>
  └─ client: ensure daemon (spawn if needed) ── dial socket ── send Request
                                                    │
daemon.Run
  └─ preflight.Run
       ├─ check ollama binary in PATH (detect GPU vendor → install hint)
       ├─ check ollama daemon reachable (/api/tags)
       ├─ (first healthy run) detect VRAM, suggest context_length, headroom msg
       ├─ (no config) run first-run wizard → write config.yaml
       ├─ ensure configured models are pulled (prompt to `ollama pull`)
       └─ if web.enabled: searxng.Ensure (Docker + container + health check)
  └─ open daemon log, build Ollama client, NewServer(...).Serve(socket)
```

`preflight.Run` returns the (possibly wizard-rewritten) config and a fatal error
if the _core_ (Ollama) path can't be satisfied — but web-search failures only
disable web search, never the whole app.

---

## Daemon lifecycle & restarting it

The daemon is a long-lived process spawned lazily by the first client call (or
started manually / via systemd). It holds the config, Ollama client, and
per-session memory **in memory**, so a running daemon keeps serving the code and
config it started with until it exits.

It stops on its own when it has been idle for `daemon.idle_timeout_minutes`, and
a fresh one is spawned automatically on the next `ai` call. When you want the
change to apply *now* — after rebuilding the binary, or after editing settings
that are only read at daemon startup — restart it explicitly:

```bash
pkill -f 'ai daemon'
```

**What this does:** `pkill -f` matches against the full command line, and the
lazily-spawned daemon runs as `<path>/ai daemon` (see `ensureDaemon` in
`cmd/ai/main.go`, which execs `os.Executable()` with the `daemon` argument). So
this pattern terminates the running daemon process.

**Why you rarely need more than this:** you do **not** have to start the daemon
again by hand. The very next `ai <query>` dials the socket, finds it dead, and
spawns a new daemon from the current binary automatically. Running `ai daemon`
manually is only useful when you want to watch its output in the foreground.

Notes and caveats:

- Rebuilding the binary (`go build -o ~/go/bin/ai ./cmd/ai`) does **not** affect
  the already-running daemon; it keeps executing the old code in memory until
  restarted. The client is a fresh process each call, so client-only changes
  (arg parsing, Markdown rendering) take effect without a restart.
- Runtime commands like `ai model-add` / `ai model-rm` update the live daemon's
  state directly, so those specifically do **not** need a restart.
- If you run the daemon as a systemd user unit instead of lazy-spawn, use
  `systemctl --user restart ai-shell` rather than `pkill`.

---

## Request / response protocol

`internal/daemon/protocol.go` defines newline-delimited JSON over the socket.

**Request types:** `query`, `model_switch`, `model_current`, `model_list`,
`model_add`, `model_remove`, `execute_confirm`.

**Response types:** `chunk` (streamed token), `searching` (web indicator),
`notice` (non-fatal info, e.g. web unavailable), `done`, `error`,
`model`, `model_list`, `model_switch`, `model_add`, `model_remove`,
`suggestion`.

The client renders `chunk`s verbatim as they arrive, then re-renders the full
answer as Markdown on a TTY (clearing the plain text first) when
`ui.render_markdown` is on. `chunk` is the only response type that carries
answer text; `suggestion` carries a structured command proposal.

---

## Query handling (the interesting part)

`Server.handleQuery` in `internal/daemon/server.go`:

1. Resolve the model (session override → session model → `default_profile`),
   validating against `ollama.models` and `/api/tags`.
2. `maybeWebSearch` — if web is enabled, run a first pass asking the model
   whether it needs live info. If it returns `{"web_search": "..."}`, query
   SearXNG and return formatted context.
3. Branch:
   - **Execution off** → `handleChatQuery`: stream a plain answer (optionally
     informed by web results).
   - **Execution on** → `handleExecQuery`: ask the model for a JSON suggestion
     (`{explanation, commands, risk}`), classify risk from config globs, send a
     `suggestion` response, read back the client's `execute_confirm`, and run
     the commands through `sh -c` if approved.

Execution and web search are **not** mutually exclusive — both contexts compose
into the final prompt.

---

## Key design decisions

- **`internal/web` vs `internal/searxng`.** `web` is a pure HTTP client for an
  already-running SearXNG instance (the "tool"). `searxng` owns the container
  (Docker lifecycle). This separation keeps the client trivial and isolates all
  `os/exec` Docker usage.
- **No silent execution.** Every command is shown and confirmed before running;
  high-risk requires typing `yes`. Every run is logged.
- **No persistent memory.** Sessions live in a mutex-guarded
  `map[sessionID]*Session`, capped by `max_history_messages`. Per-shell tokens
  come from the Zsh side, so closing a terminal naturally drops its session.
- **Minimal deps.** Risk-rule matching uses a hand-rolled glob→regexp compiler;
  Markdown rendering is a small ANSI writer. Both avoid pulling in libraries.
- **Streaming everywhere.** Ollama tokens stream through the daemon to the
  client in real time; `ollama pull` streams Ollama's own progress.

---

## Testing

Each package has a `*_test.go` covering its core behavior (config round-trips,
client framing, risk classification, session store, web parsing, preflight
checks, etc.). Run the suite with:

```bash
go test ./...
```

---

Project source: https://github.com/saeedNW/ai-shell
