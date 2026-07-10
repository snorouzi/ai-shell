package preflight

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"ai-shell/internal/config"
	"ai-shell/internal/ollama"
)

const cacheFileName = "preflight.ok"

// knownModelSizeGB approximates Q4_K_M VRAM footprints for the shipped default
// models. Used only for the informational headroom message in preflight — it is
// never used to decide whether a model can be pulled.
var knownModelSizeGB = map[string]float64{
	"qwen3:8b":         5.5,
	"qwen2.5-coder:7b": 4.7,
	"qwen3:4b":         3.0,
}

// Run performs the startup health checks from the spec. The cheap checks
// (binary-in-PATH, daemon reachable, models pulled) run every time. The
// expensive VRAM/GPU detection is skipped on subsequent healthy starts via a
// cache file. On a fully healthy first run it also writes the daemon config
// file with the first-run wizard when one does not already exist.
//
// It returns the config (possibly rewritten by the wizard) when healthy, or an
// error describing the failure so the caller can halt.
func Run(ctx context.Context, cfg config.Config, in io.Reader, out io.Writer) (config.Config, error) {
	if err := checkOllamaInstalled(out); err != nil {
		return cfg, err
	}

	client, err := ollama.New(cfg.Ollama)
	if err != nil {
		return cfg, err
	}

	if err := checkOllamaReachable(ctx, client, out); err != nil {
		return cfg, err
	}

	if !cacheExists() {
		fmt.Fprintln(out)
		vram := detectVRAM()
		suggestContextLength(vram, out)
		printHeadroom(vram, cfg, out)
		fmt.Fprintln(out)
	}

	if !configFileExists() {
		updated, err := runWizard(in, out, cfg)
		if err != nil {
			return cfg, err
		}
		if err := updated.Save(); err != nil {
			return updated, err
		}
		cfg = updated
	}

	if err := ensureModels(ctx, cfg, client, in, out); err != nil {
		return cfg, err
	}

	_ = writeCache()

	return cfg, nil
}

// checkOllamaInstalled verifies the ollama binary is on PATH. When it is not,
// it detects the GPU vendor and prints distro-agnostic install guidance, using
// Arch Linux commands only as a concrete example.
func checkOllamaInstalled(out io.Writer) error {
	if _, err := exec.LookPath("ollama"); err == nil {
		return nil
	}

	vendor := detectGPUVendor()
	fmt.Fprintln(out, "Ollama is not installed.")
	switch vendor {
	case "nvidia":
		fmt.Fprintln(out, "An NVIDIA GPU was detected — look for an Ollama build with CUDA support.")
	case "amd":
		fmt.Fprintln(out, "An AMD GPU was detected — look for an Ollama build with ROCm support.")
	default:
		fmt.Fprintln(out, "No GPU was detected — a CPU-only Ollama build will work but is slow.")
	}
	fmt.Fprintln(out, "Install Ollama via your distribution's package manager, then enable and start its service.")
	fmt.Fprintln(out, "Example (Arch Linux):")
	fmt.Fprintln(out, "  sudo pacman -S ollama-cuda   # NVIDIA (use ollama-rocm for AMD, ollama for CPU)")
	fmt.Fprintln(out, "  sudo systemctl enable --now ollama")
	fmt.Fprintln(out, "Find the equivalent package and service name for your distribution.")
	return fmt.Errorf("ollama is not installed")
}

// checkOllamaReachable confirms the Ollama daemon answers /api/tags.
func checkOllamaReachable(ctx context.Context, client *ollama.Client, out io.Writer) error {
	if _, err := client.Tags(ctx); err != nil {
		fmt.Fprintln(out, "Ollama is installed but the daemon is not reachable.")
		fmt.Fprintln(out, "Start the Ollama service (it must be running for AI Shell to work).")
		fmt.Fprintln(out, "Example (Arch Linux with systemd):")
		fmt.Fprintln(out, "  sudo systemctl status ollama")
		fmt.Fprintln(out, "  sudo systemctl enable --now ollama")
		fmt.Fprintln(out, "Adapt these to your distribution's service manager.")
		return fmt.Errorf("ollama daemon unreachable: %w", err)
	}
	return nil
}

// ensureModels lists configured models and pulls any that are missing.
func ensureModels(ctx context.Context, cfg config.Config, client *ollama.Client, in io.Reader, out io.Writer) error {
	installed, err := client.Tags(ctx)
	if err != nil {
		return err
	}
	installedSet := make(map[string]bool, len(installed))
	for _, m := range installed {
		if m.Name != "" {
			installedSet[m.Name] = true
		}
		if m.Model != "" {
			installedSet[m.Model] = true
		}
	}

	var missing []string
	for _, tag := range configuredModelTags(cfg.Ollama.Models) {
		if !installedSet[tag] {
			missing = append(missing, tag)
		}
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "Configured models:")
	for _, tag := range configuredModelTags(cfg.Ollama.Models) {
		status := "[unavailable]"
		if installedSet[tag] {
			status = "[available]"
		}
		if size := knownModelSizeGB[tag]; size > 0 {
			fmt.Fprintf(out, "  %s %s (~%.1f GB VRAM)\n", status, tag, size)
		} else {
			fmt.Fprintf(out, "  %s %s\n", status, tag)
		}
	}

	if len(missing) == 0 {
		return nil
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "The following configured models are not pulled yet:")
	for _, tag := range missing {
		if size := knownModelSizeGB[tag]; size > 0 {
			fmt.Fprintf(out, "  - %s (~%.1f GB VRAM)\n", tag, size)
		} else {
			fmt.Fprintf(out, "  - %s\n", tag)
		}
	}

	fmt.Fprintln(out)
	if !promptBool(in, out, "Pull them now? ", true) {
		fmt.Fprintln(out, "Pull the missing models manually, for example:")
		for _, tag := range missing {
			fmt.Fprintf(out, "  ollama pull %s\n", tag)
		}
		return fmt.Errorf("missing models: %s", strings.Join(missing, ", "))
	}

	for _, tag := range missing {
		fmt.Fprintf(out, "Pulling %s...\n", tag)
		if err := ollama.Pull(ctx, tag, out, out); err != nil {
			return fmt.Errorf("failed to pull %s: %w", tag, err)
		}
	}
	return nil
}

// detectGPUVendor reports nvidia, amd, or none using the same signals the spec
// describes: nvidia-smi presence, lspci output, rocminfo presence, and amdgpu
// binding from lspci -k.
func detectGPUVendor() string {
	if _, err := exec.LookPath("nvidia-smi"); err == nil {
		return "nvidia"
	}
	lspci := strings.ToLower(runCommand("lspci"))
	if strings.Contains(lspci, "nvidia") {
		return "nvidia"
	}
	if _, err := exec.LookPath("rocminfo"); err == nil {
		return "amd"
	}
	if strings.Contains(lspci, "amd") {
		if strings.Contains(strings.ToLower(runCommand("lspci", "-k")), "amdgpu") {
			return "amd"
		}
	}
	return "none"
}

// detectVRAM returns the detected total GPU VRAM in GiB, or 0 when it cannot be
// determined. It prefers nvidia-smi and falls back to rocm-smi.
func detectVRAM() float64 {
	if _, err := exec.LookPath("nvidia-smi"); err == nil {
		if v := parseNvidiaSMI(); v > 0 {
			return v
		}
	}
	if v := parseRocmSMI(); v > 0 {
		return v
	}
	return 0
}

func parseNvidiaSMI() float64 {
	out := strings.TrimSpace(runCommand("nvidia-smi", "--query-gpu=memory.total", "--format=csv,noheader"))
	if out == "" {
		return 0
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return 0
	}
	miB, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return miB / 1024
}

func parseRocmSMI() float64 {
	for _, line := range strings.Split(runCommand("rocm-smi", "--showmeminfo", "vram"), "\n") {
		if strings.Contains(line, "VRAM Total Memory (B):") {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			bytes, err := strconv.ParseFloat(fields[len(fields)-1], 64)
			if err != nil {
				continue
			}
			return bytes / (1024 * 1024 * 1024)
		}
	}
	return 0
}

// suggestContextLength prints the VRAM-tier suggestion. Informational only —
// written to config only via the wizard or manual edits.
func suggestContextLength(vram float64, out io.Writer) {
	if vram <= 0 {
		return
	}
	var tier int
	switch {
	case vram <= 6:
		tier = 4096
	case vram <= 12:
		tier = 8192
	default:
		tier = 16384
	}
	fmt.Fprintf(out, "Detected ~%.1f GB VRAM. Suggested ollama.context_length: %d (edit config to apply).\n", vram, tier)
}

// printHeadroom compares detected VRAM against the known sizes of the configured
// default models and prints one of two advisory messages.
func printHeadroom(vram float64, cfg config.Config, out io.Writer) {
	if vram <= 0 {
		return
	}
	var total float64
	for _, tag := range cfg.Ollama.Models {
		if size, ok := knownModelSizeGB[tag]; ok {
			total += size
		}
	}
	if total <= vram {
		fmt.Fprintln(out, "You can upgrade to larger models via ollama.models in config if you want higher quality.")
	} else {
		fmt.Fprintln(out, "Your system may struggle with the defaults. Consider lighter models via config, or expect slower/CPU-offloaded inference.")
	}
}

// runWizard interactively collects model tags, web-search and execution
// preferences, then returns the updated config (caller writes it).
func runWizard(in io.Reader, out io.Writer, cfg config.Config) (config.Config, error) {
	fmt.Fprintln(out, "Welcome to AI Shell. Let's create your config (~/.config/ai-shell/config.yaml).")
	fmt.Fprintln(out)

	updated := cfg
	models := make(map[string]string, len(cfg.Ollama.Models))
	for _, profile := range sortedModelTags(cfg.Ollama.Models) {
		current := cfg.Ollama.Models[profile]
		tag := promptLine(in, out, fmt.Sprintf("Model for profile %q [%s]: ", profile, current), current)
		if tag == "" {
			tag = current
		}
		models[profile] = tag
	}
	updated.Ollama.Models = models

	updated.Web.Enabled = promptBool(in, out, "Enable web search (SearXNG)? ", true)
	if updated.Web.Enabled {
		updated.Web.SearxngURL = promptLine(in, out,
			fmt.Sprintf("SearXNG URL [%s]: ", cfg.Web.SearxngURL), cfg.Web.SearxngURL)
	}
	updated.Execution.Enabled = promptBool(in, out, "Enable command execution? ", false)

	fmt.Fprintln(out)
	fmt.Fprintln(out, "Config written. You can change any setting later by editing the config file.")
	return updated, nil
}

func promptLine(in io.Reader, out io.Writer, prompt, def string) string {
	fmt.Fprint(out, prompt)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return def
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func promptBool(in io.Reader, out io.Writer, prompt string, def bool) bool {
	suffix := " [Y/n] "
	if !def {
		suffix = " [y/N] "
	}
	fmt.Fprint(out, prompt+suffix)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	case "n", "no":
		return false
	default:
		return def
	}
}

func sortedModelTags(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// configuredModelTags returns the unique model tags (map values) from
// ollama.models, sorted. The map is keyed by profile name, so callers that want
// the actual model identifiers to compare against /api/tags must use the values.
func configuredModelTags(m map[string]string) []string {
	seen := make(map[string]struct{}, len(m))
	tags := make([]string, 0, len(m))
	for _, tag := range m {
		if _, dup := seen[tag]; dup {
			continue
		}
		seen[tag] = struct{}{}
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags
}

func runCommand(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func configFileExists() bool {
	path, err := config.Path()
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func stateDir() (string, error) {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "ai-shell"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "ai-shell"), nil
}

func cachePath() (string, error) {
	dir, err := stateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, cacheFileName), nil
}

func cacheExists() bool {
	path, err := cachePath()
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func writeCache() error {
	path, err := cachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("preflight passed\n"), 0o644)
}
