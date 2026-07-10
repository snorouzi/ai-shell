package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLoadFileMissingReturnsExpandedDefaults(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	cfg, err := LoadFile(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("LoadFile returned error: %v", err)
	}

	if cfg.App.Name != "ai-shell" {
		t.Fatalf("App.Name = %q, want ai-shell", cfg.App.Name)
	}
	if cfg.Ollama.Models["coding"] != "qwen2.5-coder:7b" {
		t.Fatalf("coding model = %q", cfg.Ollama.Models["coding"])
	}
	if cfg.Daemon.SocketPath != "/run/user/1000/ai-shell.sock" {
		t.Fatalf("SocketPath = %q", cfg.Daemon.SocketPath)
	}
	if cfg.Execution.Enabled {
		t.Fatal("execution should default to disabled")
	}
}

func TestSaveFileAndLoadFileRoundTrip(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	cfg := Default()
	cfg.Ollama.Models["general"] = "custom:latest"
	cfg.Daemon.LogFile = "~/logs/daemon.log"

	if err := cfg.SaveFile(path); err != nil {
		t.Fatalf("SaveFile returned error: %v", err)
	}

	loaded, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile returned error: %v", err)
	}

	if loaded.Ollama.Models["general"] != "custom:latest" {
		t.Fatalf("general model = %q", loaded.Ollama.Models["general"])
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir returned error: %v", err)
	}
	wantLog := filepath.Join(home, "logs", "daemon.log")
	if loaded.Daemon.LogFile != wantLog {
		t.Fatalf("Daemon.LogFile = %q, want %q", loaded.Daemon.LogFile, wantLog)
	}
}

func TestAddProfileCreatesFileFromDefaults(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	if err := AddProfile(path, "vision", "llava:7b"); err != nil {
		t.Fatalf("AddProfile returned error: %v", err)
	}

	loaded, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile returned error: %v", err)
	}
	if loaded.Ollama.Models["vision"] != "llava:7b" {
		t.Fatalf("vision model = %q, want llava:7b", loaded.Ollama.Models["vision"])
	}
	// Seeded defaults must still be present.
	if loaded.Ollama.Models["general"] != "qwen3:8b" {
		t.Fatalf("general model = %q, want qwen3:8b", loaded.Ollama.Models["general"])
	}
}

func TestAddProfileUpdatesExistingAndPreservesTemplates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := "ollama:\n" +
		"  default_profile: general\n" +
		"  models:\n" +
		"    general: qwen3:8b\n" +
		"daemon:\n" +
		"  socket_path: ${XDG_RUNTIME_DIR}/ai-shell.sock\n" +
		"  log_file: ~/.local/state/ai-shell/daemon.log\n"
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	// Add a new profile and update an existing one.
	if err := AddProfile(path, "coding", "qwen2.5-coder:7b"); err != nil {
		t.Fatalf("AddProfile(coding) returned error: %v", err)
	}
	if err := AddProfile(path, "general", "llama3:8b"); err != nil {
		t.Fatalf("AddProfile(general) returned error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	got := string(data)

	// Templated paths must survive verbatim (not expanded/clobbered).
	if !strings.Contains(got, "${XDG_RUNTIME_DIR}/ai-shell.sock") {
		t.Fatalf("templated socket_path was clobbered:\n%s", got)
	}
	if !strings.Contains(got, "~/.local/state/ai-shell/daemon.log") {
		t.Fatalf("templated log_file was clobbered:\n%s", got)
	}

	// Verify the values through a raw (non-expanding) parse.
	var doc struct {
		Ollama struct {
			Models map[string]string `yaml:"models"`
		} `yaml:"ollama"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if doc.Ollama.Models["coding"] != "qwen2.5-coder:7b" {
		t.Fatalf("coding model = %q", doc.Ollama.Models["coding"])
	}
	if doc.Ollama.Models["general"] != "llama3:8b" {
		t.Fatalf("general model = %q, want updated llama3:8b", doc.Ollama.Models["general"])
	}
}

func TestAddProfileRejectsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := AddProfile(path, "  ", "llava:7b"); err == nil {
		t.Fatal("AddProfile with empty profile should error")
	}
	if err := AddProfile(path, "vision", "  "); err == nil {
		t.Fatal("AddProfile with empty model should error")
	}
}

func TestRemoveProfileDeletesAndPreservesRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := "ollama:\n" +
		"  default_profile: general\n" +
		"  models:\n" +
		"    general: qwen3:8b\n" +
		"    vision: llava:7b\n" +
		"daemon:\n" +
		"  socket_path: ${XDG_RUNTIME_DIR}/ai-shell.sock\n"
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	if err := RemoveProfile(path, "vision"); err != nil {
		t.Fatalf("RemoveProfile returned error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	got := string(data)
	if strings.Contains(got, "vision") || strings.Contains(got, "llava:7b") {
		t.Fatalf("vision profile was not removed:\n%s", got)
	}
	if !strings.Contains(got, "general: qwen3:8b") {
		t.Fatalf("general profile was clobbered:\n%s", got)
	}
	if !strings.Contains(got, "${XDG_RUNTIME_DIR}/ai-shell.sock") {
		t.Fatalf("templated socket_path was clobbered:\n%s", got)
	}
}

func TestLoadFileTreatsModelsAsAuthoritative(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	// A file that lists only one profile must NOT get the other defaults merged
	// back in — otherwise removing a built-in profile would never stick.
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("ollama:\n  models:\n    general: qwen3:8b\n"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	loaded, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile returned error: %v", err)
	}
	if len(loaded.Ollama.Models) != 1 {
		t.Fatalf("Models = %#v, want only general", loaded.Ollama.Models)
	}
	if _, ok := loaded.Ollama.Models["coding"]; ok {
		t.Fatal("coding default should not be merged back in")
	}
}

func TestRemoveProfileMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("ollama:\n  models:\n    general: qwen3:8b\n"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if err := RemoveProfile(path, "nope"); err == nil {
		t.Fatal("RemoveProfile of a missing profile should error")
	}
	if err := RemoveProfile(filepath.Join(t.TempDir(), "absent.yaml"), "general"); err == nil {
		t.Fatal("RemoveProfile on a missing file should error")
	}
}
