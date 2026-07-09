package main

import (
	"bytes"
	"testing"

	"ai-shell/internal/config"
)

func TestParseArgsBuildsPrompt(t *testing.T) {
	opts, err := parseArgs([]string{"explain", "this", "error"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	if opts.prompt != "explain this error" {
		t.Fatalf("prompt = %q", opts.prompt)
	}
	if opts.modelOverride != "" {
		t.Fatalf("modelOverride = %q, want empty", opts.modelOverride)
	}
}

func TestParseArgsSupportsModelOverride(t *testing.T) {
	opts, err := parseArgs([]string{"--model", "qwen2.5-coder:7b", "fix", "this"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	if opts.modelOverride != "qwen2.5-coder:7b" {
		t.Fatalf("modelOverride = %q", opts.modelOverride)
	}
	if opts.prompt != "fix this" {
		t.Fatalf("prompt = %q", opts.prompt)
	}
}

func TestParseArgsRejectsLaterCommands(t *testing.T) {
	if _, err := parseArgs([]string{"models"}, &bytes.Buffer{}); err == nil {
		t.Fatal("parseArgs returned nil error")
	}
}

func TestSelectModelUsesDefaultProfile(t *testing.T) {
	cfg := config.Default().Ollama

	model, err := selectModel(cfg, "")
	if err != nil {
		t.Fatalf("selectModel returned error: %v", err)
	}

	if model != "qwen3:8b" {
		t.Fatalf("model = %q, want qwen3:8b", model)
	}
}

func TestSelectModelUsesOverride(t *testing.T) {
	model, err := selectModel(config.Default().Ollama, "custom:latest")
	if err != nil {
		t.Fatalf("selectModel returned error: %v", err)
	}

	if model != "custom:latest" {
		t.Fatalf("model = %q, want custom:latest", model)
	}
}

func TestSelectModelRejectsMissingDefaultProfile(t *testing.T) {
	cfg := config.Default().Ollama
	cfg.DefaultProfile = "missing"

	if _, err := selectModel(cfg, ""); err == nil {
		t.Fatal("selectModel returned nil error")
	}
}
