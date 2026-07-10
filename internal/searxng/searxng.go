// Package searxng manages the lifecycle of the SearXNG container that backs AI
// Shell's web search. It is deliberately separate from internal/web, which
// stays a pure HTTP client for querying an already-running instance.
//
// Everything here only runs when web.enabled is true in config. When anything
// goes wrong (Docker missing, port taken, container won't start, never
// healthy), the failure is reported to the user and web search is disabled for
// the session rather than aborting AI Shell as a whole.
package searxng

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ai-shell/internal/config"
)

const (
	containerName = "ai-shell-searxng"
	image         = "searxng/searxng"
	defaultPort   = 8081
	healthTimeout = 30 * time.Second
)

// Ensure brings up a SearXNG container for the given web config and waits until
// it is serving. It returns the (possibly rewritten) config and whether web
// search is ready. A false second value means the caller should treat web
// search as disabled for this session; the reason is already printed to out.
func Ensure(ctx context.Context, cfg config.WebConfig, out io.Writer) (config.WebConfig, bool) {
	if !cfg.Enabled {
		return cfg, false
	}

	if !dockerInstalled() {
		fmt.Fprintln(out, "Web search needs Docker, but it is not installed.")
		fmt.Fprintln(out, "Install Docker, then try again (web search stays off for now):")
		fmt.Fprintln(out, "  sudo pacman -S docker && sudo systemctl enable --now docker")
		fmt.Fprintln(out, "Other distros: follow https://docs.docker.com/engine/install/")
		return cfg, false
	}
	if !dockerRunning(ctx) {
		fmt.Fprintln(out, "Docker is installed but its daemon is not running.")
		fmt.Fprintln(out, "Start it, then try again (web search stays off for now):")
		fmt.Fprintln(out, "  sudo systemctl enable --now docker")
		return cfg, false
	}

	port := cfg.SearxngPort
	if port == 0 {
		port = defaultPort
		cfg.SearxngPort = port
	}

	// Is our own managed container already present? If so the port is ours and
	// there is nothing to conflict-check — ensureContainer starts/uses it below.
	ourExists, _ := containerState(ctx)

	reuse := ""
	if !ourExists {
		// Our container isn't managing this port yet. A free port is fine. If
		// the port is taken but by a SearXNG container, reuse it instead of
		// failing. Any other occupant is a genuine conflict.
		if !portFree(port) {
			if name := searxngContainerOnPort(ctx, port); name != "" {
				fmt.Fprintf(out, "Port %d is already used by another SearXNG container (%s) — reusing it for web search.\n", port, name)
				reuse = name
			} else {
				fmt.Fprintf(out, "Web search port %d is already in use by another process.\n", port)
				fmt.Fprintln(out, "Change web.searxng_port in config to a free port; web search stays off for now.")
				return cfg, false
			}
		}
	}

	fmt.Fprintln(out, "Setting up SearXNG for web search...")
	if err := ensureSettings(port); err != nil {
		fmt.Fprintf(out, "Could not write SearXNG config: %v\n", err)
		fmt.Fprintln(out, "Web search will stay off for this session.")
		return cfg, false
	}

	if reuse != "" {
		// Reusing a foreign SearXNG container: make sure it survives reboots,
		// the same as ensureContainer does for our own container.
		ensureRestartPolicy(ctx, out, reuse)
	} else if err := ensureContainer(ctx, port, out); err != nil {
		fmt.Fprintf(out, "Could not start SearXNG container: %v\n", err)
		fmt.Fprintln(out, "Web search will stay off for this session.")
		return cfg, false
	}

	if !waitHealthy(ctx, port, out) {
		fmt.Fprintln(out, "Web search will stay off for this session.")
		return cfg, false
	}

	fmt.Fprintf(out, "SearXNG is ready on port %d.\n", port)
	return cfg, true
}

// ensureSettings guarantees a known-good settings.yml exists in the ai-shell
// SearXNG config directory before any container is started. It is written
// explicitly (never relying on SearXNG's default-generation step) so we never
// hit the previously-seen crash from a missing secret_key. An existing file is
// left untouched unless the configured port has changed, in which case it is
// regenerated.
func ensureSettings(port int) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	settingsPath := filepath.Join(dir, "settings.yml")
	portPath := filepath.Join(dir, "port.txt")

	if _, err := os.Stat(settingsPath); err == nil {
		// Existing file: only rewrite when the configured port differs, so we
		// don't clobber user edits on every start.
		if data, err := os.ReadFile(portPath); err == nil && strings.TrimSpace(string(data)) == strconv.Itoa(port) {
			return nil
		}
	}

	key, err := generateSecretKey()
	if err != nil {
		return err
	}
	content := fmt.Sprintf(settingsTemplate, key, port)
	if err := os.WriteFile(settingsPath, []byte(content), 0o644); err != nil {
		return err
	}
	return os.WriteFile(portPath, []byte(strconv.Itoa(port)), 0o644)
}

// generateSecretKey returns a 64-character hex string (32 random bytes), the
// format SearXNG expects for server.secret_key. It is Go-native and dependency
// free — no shelling out to openssl.
func generateSecretKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ensureContainer makes sure the ai-shell-searxng container is running,
// creating it (with our config mounted) when missing and starting it when
// merely stopped.
func ensureContainer(ctx context.Context, port int, out io.Writer) error {
	exists, running := containerState(ctx)
	switch {
	case running:
		fmt.Fprintln(out, "SearXNG container already running.")
		ensureRestartPolicy(ctx, out, containerName)
		return nil
	case exists:
		fmt.Fprintln(out, "Starting existing SearXNG container...")
		if _, err := dockerOut(ctx, "start", containerName); err != nil {
			return fmt.Errorf("failed to start container: %w", err)
		}
		ensureRestartPolicy(ctx, out, containerName)
		return nil
	default:
		fmt.Fprintln(out, "Creating and starting SearXNG container...")
		dir, err := configDir()
		if err != nil {
			return err
		}
		args := []string{
			"run", "-d",
			"--name", containerName,
			"--dns", "8.8.8.8",
			"-p", fmt.Sprintf("%d:8080", port),
			"-v", dir + ":/etc/searxng",
			"-e", fmt.Sprintf("SEARXNG_BASE_URL=http://localhost:%d/", port),
			"--restart", "unless-stopped",
			image,
		}
		outStr, err := dockerOut(ctx, args...)
		if err != nil {
			if strings.Contains(outStr, "port is already allocated") || strings.Contains(outStr, "bind") {
				return fmt.Errorf("port %d is already in use; change web.searxng_port in config: %w", port, err)
			}
			return fmt.Errorf("failed to create container: %w", err)
		}
		return nil
	}
}

// containerState reports whether the managed container exists and whether it is
// currently running.
func containerState(ctx context.Context) (exists, running bool) {
	out, _ := dockerOut(ctx, "ps", "-a", "--filter", "name="+containerName, "--format", "{{.Names}}")
	if strings.TrimSpace(out) != containerName {
		return false, false
	}
	out, _ = dockerOut(ctx, "ps", "--filter", "name="+containerName, "--format", "{{.Names}}")
	return true, strings.TrimSpace(out) == containerName
}

// waitHealthy polls the container's HTTP endpoint until it answers or the
// health timeout elapses. Container startup is not instant, so we allow a
// generous window before giving up.
func waitHealthy(ctx context.Context, port int, out io.Writer) bool {
	url := fmt.Sprintf("http://localhost:%d/", port)
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(healthTimeout)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return false
		}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return true
			}
		}
		time.Sleep(time.Second)
	}

	fmt.Fprintln(out, "SearXNG did not become healthy in time. Recent container logs:")
	fmt.Fprintln(out, dockerLogs(ctx))
	return false
}

// portFree reports whether nothing is already bound to the given TCP port.
// Docker would fail to publish the port otherwise, so we surface this early
// with a clear message instead of letting docker run error out opaquely.
// portFree reports whether nothing is already bound to the given TCP port.
// Docker would fail to publish the port otherwise, so we surface this early
// with a clear message instead of letting docker run error out opaquely.
func portFree(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

// searxngContainerOnPort returns the name of a running container that publishes
// the given port and is based on a SearXNG image, or "" if none. This lets us
// reuse an existing SearXNG instance (including one the user started by hand)
// instead of treating its port as a conflict.
func searxngContainerOnPort(ctx context.Context, port int) string {
	out, _ := dockerOut(ctx, "ps",
		"--filter", fmt.Sprintf("publish=%d", port),
		"--format", "{{.Names}}\t{{.Image}}")
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimSpace(fields[0])
		image := strings.ToLower(strings.TrimSpace(fields[1]))
		if name != "" && strings.Contains(image, "searxng") {
			return name
		}
	}
	return ""
}

// ensureRestartPolicy guarantees the named container comes back up after a
// Docker daemon / system reboot. The run command sets --restart unless-stopped
// on creation, but a pre-existing container may lack it, so we re-apply it
// idempotently whenever we touch the container. This is best-effort: a failure
// here does not block web search.
func ensureRestartPolicy(ctx context.Context, out io.Writer, name string) {
	if _, err := dockerOut(ctx, "update", "--restart", "unless-stopped", name); err != nil {
		fmt.Fprintf(out, "Warning: could not set container restart policy: %v\n", err)
	}
}

func dockerInstalled() bool {
	_, err := exec.LookPath("docker")
	return err == nil
}

func dockerRunning(ctx context.Context) bool {
	_, err := dockerOut(ctx, "info")
	return err == nil
}

func dockerLogs(ctx context.Context) string {
	out, _ := dockerOut(ctx, "logs", containerName, "--tail", "30")
	return out
}

// dockerOut runs a docker subcommand and captures combined stdout/stderr, so
// callers can inspect error text (e.g. port-binding conflicts).
func dockerOut(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var b strings.Builder
	cmd.Stdout = &b
	cmd.Stderr = &b
	err := cmd.Run()
	return b.String(), err
}

func configDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ai-shell", "searxng"), nil
}

// settingsTemplate is the minimal, known-good SearXNG configuration. The two
// verbs are the generated secret_key (%s) and the configured port (%d).
// use_default_settings: true makes SearXNG merge this with its built-in
// defaults, so we don't need to reproduce the entire upstream file.
const settingsTemplate = `use_default_settings: true

general:
  debug: false
  instance_name: "AI Shell Search"

server:
  secret_key: "%s"
  bind_address: "0.0.0.0"
  port: 8080
  base_url: "http://localhost:%d/"
  image_proxy: false

search:
  formats:
    - html
    - json
  safe_search: 0
  autocomplete: ""
  default_lang: "en"

outgoing:
  request_timeout: 10.0
  max_request_timeout: 15.0
  pool_connections: 100
  pool_maxsize: 20

ui:
  static_use_hash: true
`
