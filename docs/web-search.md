# Web search (SearXNG)

AI Shell can augment answers with live web results. Unlike the rest of the
system, this part relies on an external service — a
[SearXNG](https://searxng.org/) instance — but AI Shell **manages that
container for you**. There is nothing to install or run by hand: flip a config
flag and the daemon handles the rest on its next start.

This is different from the Ollama runtime, which runs natively on the host.
Docker is reserved _only_ for this optional auxiliary service.

---

## How it works

When `web.enabled: true`, the daemon's startup preflight (`internal/searxng`)
runs the following flow. Every step only happens because web search is on; if
`web.enabled` is `false`, none of this runs.

1. **Docker check.** Is the `docker` binary present and is the daemon running
   (`docker info`)? If not, AI Shell prints the install/start command and falls
   back to running with web search **disabled for this session** rather than
   blocking all of AI Shell.

   ```bash
   sudo pacman -S docker && sudo systemctl enable --now docker
   ```

2. **SearXNG config file.** Before any container is started, a known-good
   `~/.config/ai-shell/searxng/settings.yml` is written directly. AI Shell does
   _not_ rely on SearXNG's own default-generation step (which previously crashed
   with a missing `secret_key`). The file uses `use_default_settings: true` so
   SearXNG merges it with its built-in defaults.
   - `secret_key` is generated in Go (`crypto/rand`, 32 random bytes, hex) —
     no shelling out to `openssl`.
   - `base_url` and `port` are interpolated from `web.searxng_port`.
   - If the file already exists, it is left alone — **unless** `searxng_port`
     changed since it was written, in which case it is regenerated.

3. **Container lifecycle.** The `ai-shell-searxng` container is reconciled:
   - Already running and healthy → skip creation.
   - Exists but stopped → `docker start ai-shell-searxng`.
   - Doesn't exist → `docker run -d` (see below), only after the config file is
     confirmed on disk.

   ```bash
   docker run -d \
     --name ai-shell-searxng \
     --dns 8.8.8.8 \
     -p <searxng_port>:8080 \
     -v ~/.config/ai-shell/searxng:/etc/searxng \
     -e SEARXNG_BASE_URL=http://localhost:<searxng_port>/ \
     --restart unless-stopped \
     searxng/searxng
   ```

   > The container's **internal** port stays `8080`; only the host port (mapped
   > with `-p`) comes from `searxng_port`. So a single free host port is all you
   > need to change.

4. **Port conflict.** If `searxng_port` is already taken by something other
   than a SearXNG container, AI Shell reports it clearly and tells you to change
   `web.searxng_port` in config — it will **not** silently pick a different
   port. (If the occupied port belongs to another SearXNG container, AI Shell
   reuses that one instead of failing.)

5. **Health check.** Before declaring web search ready, the daemon polls
   `http://localhost:<searxng_port>/` with a short retry loop (up to ~30 s,
   since container startup isn't instant). If it never becomes healthy, web
   search is disabled for the session with a clear log message, and the last 30
   lines of `docker logs ai-shell-searxng` are printed to help diagnose.

---

## Enabling it

Edit `~/.config/ai-shell/config.yaml`:

```yaml
web:
  enabled: true
  provider: searxng
  searxng_port: 8081
```

On your next `ai` command, the daemon sets everything up. To turn it all off,
set `web.enabled: false` — AI Shell won't touch Docker at all.

---

## How the model uses it

Web search is a **tool**, not an always-on feature. When enabled, the daemon
adds a system instruction telling the model it may call a `web_search` tool.
The model decides per query whether it needs current information:

1. The model replies with `{"web_search": "your search query"}` (or answers
   directly if it doesn't need the web).
2. If it asked to search, AI Shell queries the local SearXNG JSON API at
   `http://localhost:<searxng_port>/search?format=json`.
3. Results are folded back into the prompt, and the final answer is generated
   with that context.

If the search fails for any reason, AI Shell falls back to answering without
live results — the whole query never aborts.

---

## Troubleshooting

| Symptom                                                   | Fix                                                                                                                                                                 |
| --------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------- |
| "Web search needs Docker, but it is not installed."       | Install and start Docker: `sudo pacman -S docker && sudo systemctl enable --now docker` (adapt for your distro).                                                    |
| "Web search port N is already in use by another process." | Change `web.searxng_port` to a free port (check `ss -tulpn                                                                                                          | grep LISTEN`). |
| Web search says "disabled for this session"               | Read the emitted notice — usually Docker not ready, port conflict, or the container never became healthy. `docker logs ai-shell-searxng --tail 30` shows the cause. |
| Want to fully reset the container                         | `docker rm -f ai-shell-searxng`, then run `ai` again (web enabled) to recreate it.                                                                                  |

Relevant paths:

- SearXNG config: `~/.config/ai-shell/searxng/settings.yml`
- Container name: `ai-shell-searxng`
- Image: `searxng/searxng`

---

## FAQ

**Can I run SearXNG on a different host port?**
Yes. Change `searxng_port` in config — the container's internal `8080` stays
the same, only the host mapping changes. Pick any free port.

**Do I need Docker Compose or a Dockerfile?**
No. A single `docker run`/`docker start` via `os/exec` is used, consistent with
how the project shells out to the `ollama` CLI. No orchestration files are
added.

**What was `searxng_url`?**
An earlier design had you point AI Shell at a manually-started instance via a
full URL. That was replaced by `searxng_port`: AI Shell now owns the container
and therefore the URL. See
[`configuration.md`](configuration.md#web).

---

Project source: https://github.com/saeedNW/ai-shell
