package main

import (
	"bytes"
	"testing"

	"ai-shell/internal/daemon"
)

func TestParseArgsBuildsQuery(t *testing.T) {
	cmd, err := parseArgs([]string{"explain", "this", "error"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	if cmd.kind != "query" {
		t.Fatalf("kind = %q, want query", cmd.kind)
	}
	if cmd.prompt != "explain this error" {
		t.Fatalf("prompt = %q", cmd.prompt)
	}
}

func TestParseArgsSupportsModelOverride(t *testing.T) {
	cmd, err := parseArgs([]string{"--model", "qwen2.5-coder:7b", "fix", "this"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	if cmd.modelOverride != "qwen2.5-coder:7b" {
		t.Fatalf("modelOverride = %q", cmd.modelOverride)
	}
	if cmd.prompt != "fix this" {
		t.Fatalf("prompt = %q", cmd.prompt)
	}
}

func TestParseArgsModelCommands(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		modelArg string
	}{
		{name: "current", args: []string{"model"}},
		{name: "reset", args: []string{"model", "reset"}, modelArg: "reset"},
		{name: "switch", args: []string{"model", "coding"}, modelArg: "coding"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := parseArgs(tt.args, &bytes.Buffer{})
			if err != nil {
				t.Fatalf("parseArgs returned error: %v", err)
			}
			if cmd.kind != "model" {
				t.Fatalf("kind = %q, want model", cmd.kind)
			}
			if cmd.modelArg != tt.modelArg {
				t.Fatalf("modelArg = %q, want %q", cmd.modelArg, tt.modelArg)
			}
		})
	}
}

func TestRequestForCommand(t *testing.T) {
	req := requestForCommand("s1", command{
		kind:          "query",
		modelOverride: "qwen3:4b",
		prompt:        "hello",
	})

	if req.Type != daemon.RequestQuery {
		t.Fatalf("Type = %q", req.Type)
	}
	if req.SessionID != "s1" {
		t.Fatalf("SessionID = %q", req.SessionID)
	}
	if req.Model != "qwen3:4b" {
		t.Fatalf("Model = %q", req.Model)
	}
}

func TestRequestForModelCurrent(t *testing.T) {
	req := requestForCommand("s1", command{kind: "model"})

	if req.Type != daemon.RequestModelCurrent {
		t.Fatalf("Type = %q", req.Type)
	}
}

func TestHandleModelListResponse(t *testing.T) {
	var out bytes.Buffer

	done, err := handleResponse(&out, daemon.Response{
		Type:         daemon.ResponseModelList,
		CurrentModel: "qwen3:8b",
		Profiles: []daemon.ModelProfile{
			{Profile: "general", Model: "qwen3:8b", Installed: true},
		},
	}, nil)
	if err != nil {
		t.Fatalf("handleResponse returned error: %v", err)
	}
	if !done {
		t.Fatal("done = false")
	}

	want := "current model: qwen3:8b\ngeneral\tqwen3:8b\tinstalled\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

func TestHandleChunkStopsSpinner(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	spinner := newThinkingSpinner(&stderr)
	spinner.Start()

	done, err := handleResponse(&stdout, daemon.Response{
		Type:    daemon.ResponseChunk,
		Content: "OK",
	}, spinner)
	if err != nil {
		t.Fatalf("handleResponse returned error: %v", err)
	}
	if done {
		t.Fatal("done = true")
	}
	if stdout.String() != "OK" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("\033[2K")) {
		t.Fatalf("stderr did not contain clear-line escape: %q", stderr.String())
	}
}
