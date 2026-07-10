package main

import (
	"bytes"
	"testing"

	"ai-shell/internal/config"
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

func TestParseArgsHelp(t *testing.T) {
	for _, args := range [][]string{
		{"--help"},
		{"-h"},
		{"help"},
	} {
		cmd, err := parseArgs(args, &bytes.Buffer{})
		if err != nil {
			t.Fatalf("parseArgs(%v) returned error: %v", args, err)
		}
		if cmd.kind != "help" {
			t.Fatalf("parseArgs(%v) kind = %q, want help", args, cmd.kind)
		}
	}
}

func TestPrintHelpMentionsCommands(t *testing.T) {
	var out bytes.Buffer
	printHelp(&out)

	for _, want := range []string{"Usage:", "model", "models", "daemon", "--model", "-h, --help"} {
		if !bytes.Contains(out.Bytes(), []byte(want)) {
			t.Fatalf("help output missing %q\n%s", want, out.String())
		}
	}
}

func TestParseArgsModelAdd(t *testing.T) {
	cmd, err := parseArgs([]string{"model-add", "vision", "llava:7b"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if cmd.kind != "model-add" {
		t.Fatalf("kind = %q, want model-add", cmd.kind)
	}
	if cmd.profileArg != "vision" {
		t.Fatalf("profileArg = %q, want vision", cmd.profileArg)
	}
	if cmd.modelArg != "llava:7b" {
		t.Fatalf("modelArg = %q, want llava:7b", cmd.modelArg)
	}
}

func TestParseArgsModelAddRequiresTwoArgs(t *testing.T) {
	for _, args := range [][]string{
		{"model-add"},
		{"model-add", "vision"},
		{"model-add", "vision", "llava:7b", "extra"},
	} {
		if _, err := parseArgs(args, &bytes.Buffer{}); err == nil {
			t.Fatalf("parseArgs(%v) expected error", args)
		}
	}
}

func TestRequestForModelAdd(t *testing.T) {
	req := requestForCommand("s1", command{
		kind:       "model-add",
		profileArg: "vision",
		modelArg:   "llava:7b",
	})
	if req.Type != daemon.RequestModelAdd {
		t.Fatalf("Type = %q, want %q", req.Type, daemon.RequestModelAdd)
	}
	if req.Profile != "vision" {
		t.Fatalf("Profile = %q", req.Profile)
	}
	if req.Model != "llava:7b" {
		t.Fatalf("Model = %q", req.Model)
	}
}

func TestHandleModelAddResponse(t *testing.T) {
	var out bytes.Buffer
	done, err := handleResponse(nil, config.Config{}, "", &out, daemon.Response{
		Type: daemon.ResponseModelAdd,
		Profiles: []daemon.ModelProfile{
			{Profile: "vision", Model: "llava:7b", Installed: false},
		},
	}, nil, &queryRenderer{})
	if err != nil {
		t.Fatalf("handleResponse returned error: %v", err)
	}
	if !done {
		t.Fatal("done = false")
	}
	if !bytes.Contains(out.Bytes(), []byte(`added profile "vision" -> llava:7b`)) {
		t.Fatalf("output missing confirmation: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("ollama pull llava:7b")) {
		t.Fatalf("output missing pull hint: %q", out.String())
	}
}

func TestParseArgsModelRemove(t *testing.T) {
	cmd, err := parseArgs([]string{"model-rm", "vision"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if cmd.kind != "model-rm" {
		t.Fatalf("kind = %q, want model-rm", cmd.kind)
	}
	if cmd.profileArg != "vision" {
		t.Fatalf("profileArg = %q, want vision", cmd.profileArg)
	}
}

func TestParseArgsModelRemoveRequiresOneArg(t *testing.T) {
	for _, args := range [][]string{
		{"model-rm"},
		{"model-rm", "vision", "extra"},
	} {
		if _, err := parseArgs(args, &bytes.Buffer{}); err == nil {
			t.Fatalf("parseArgs(%v) expected error", args)
		}
	}
}

func TestRequestForModelRemove(t *testing.T) {
	req := requestForCommand("s1", command{kind: "model-rm", profileArg: "vision"})
	if req.Type != daemon.RequestModelRemove {
		t.Fatalf("Type = %q, want %q", req.Type, daemon.RequestModelRemove)
	}
	if req.Profile != "vision" {
		t.Fatalf("Profile = %q", req.Profile)
	}
}

func TestHandleModelRemoveResponse(t *testing.T) {
	var out bytes.Buffer
	done, err := handleResponse(nil, config.Config{}, "", &out, daemon.Response{
		Type:     daemon.ResponseModelRemove,
		Profiles: []daemon.ModelProfile{{Profile: "vision"}},
	}, nil, &queryRenderer{})
	if err != nil {
		t.Fatalf("handleResponse returned error: %v", err)
	}
	if !done {
		t.Fatal("done = false")
	}
	if !bytes.Contains(out.Bytes(), []byte(`removed profile "vision"`)) {
		t.Fatalf("output missing confirmation: %q", out.String())
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

	done, err := handleResponse(nil, config.Config{}, "", &out, daemon.Response{
		Type:         daemon.ResponseModelList,
		CurrentModel: "qwen3:8b",
		Profiles: []daemon.ModelProfile{
			{Profile: "general", Model: "qwen3:8b", Installed: true},
		},
	}, nil, &queryRenderer{})
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
	spinner := newThinkingSpinner(&stderr, "Thinking...")
	spinner.Start()

	done, err := handleResponse(nil, config.Config{}, "", &stdout, daemon.Response{
		Type:    daemon.ResponseChunk,
		Content: "OK",
	}, spinner, &queryRenderer{})
	if err != nil {
		t.Fatalf("handleResponse returned error: %v", err)
	}
	if done {
		t.Fatal("done = true")
	}
	if stdout.String() != "OK" {
		t.Fatalf("stdout = %q, want %q", stdout.String(), "OK")
	}
	if !bytes.Contains(stderr.Bytes(), []byte("\033[2K")) {
		t.Fatalf("stderr did not contain clear-line escape: %q", stderr.String())
	}
}
