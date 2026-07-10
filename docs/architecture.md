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

## Request / response protocol

`internal/daemon/protocol.go` defines newline-delimited JSON over the socket.

**Request types:** `query`, `model_switch`, `model_current`, `model_list`,
`execute_confirm`.

**Response types:** `chunk` (streamed token), `searching` (web indicator),
`notice` (non-fatal info, e.g. web unavailable), `done`, `error`,
`model`, `model_list`, `model_switch`, `suggestion`.

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
