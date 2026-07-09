package config

import (
	"os"
	"path/filepath"
	"testing"
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
