package daemon

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"ai-shell/internal/config"
	"ai-shell/internal/ollama"
)

type fakeOllama struct {
	models []ollama.Model
	prompt string
}

func (f *fakeOllama) Generate(ctx context.Context, req ollama.GenerateRequest, onChunk func(ollama.GenerateChunk) error) error {
	f.prompt = req.Prompt
	if err := onChunk(ollama.GenerateChunk{Response: "hello "}); err != nil {
		return err
	}
	if err := onChunk(ollama.GenerateChunk{Response: "world"}); err != nil {
		return err
	}
	return onChunk(ollama.GenerateChunk{Done: true})
}

func (f *fakeOllama) Tags(ctx context.Context) ([]ollama.Model, error) {
	return f.models, nil
}

func TestHandleQueryStreamsAndStoresHistory(t *testing.T) {
	cfg := config.Default()
	fake := &fakeOllama{}
	server := NewServer(cfg, fake, nil)

	responses := exchange(t, server, Request{
		Type:      RequestQuery,
		SessionID: "s1",
		Prompt:    "say hello",
	})

	if len(responses) != 3 {
		t.Fatalf("len(responses) = %d, want 3: %#v", len(responses), responses)
	}
	if responses[0].Type != ResponseChunk || responses[0].Content != "hello " {
		t.Fatalf("responses[0] = %#v", responses[0])
	}
	if responses[1].Type != ResponseChunk || responses[1].Content != "world" {
		t.Fatalf("responses[1] = %#v", responses[1])
	}
	if responses[2].Type != ResponseDone {
		t.Fatalf("responses[2] = %#v", responses[2])
	}

	messages := server.sessions.Messages("s1")
	if len(messages) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(messages))
	}
	if messages[1].Content != "hello world" {
		t.Fatalf("assistant message = %q", messages[1].Content)
	}
}

func TestModelSwitchAcceptsInstalledProfile(t *testing.T) {
	cfg := config.Default()
	fake := &fakeOllama{
		models: []ollama.Model{{Name: "qwen2.5-coder:7b"}},
	}
	server := NewServer(cfg, fake, nil)

	responses := exchange(t, server, Request{
		Type:      RequestModelSwitch,
		SessionID: "s1",
		Model:     "coding",
	})

	if len(responses) != 1 {
		t.Fatalf("len(responses) = %d, want 1", len(responses))
	}
	if responses[0].Type != ResponseModelSwitch {
		t.Fatalf("Type = %q", responses[0].Type)
	}
	if responses[0].CurrentModel != "qwen2.5-coder:7b" {
		t.Fatalf("CurrentModel = %q", responses[0].CurrentModel)
	}
}

func TestModelListReportsInstalledStatus(t *testing.T) {
	cfg := config.Default()
	fake := &fakeOllama{
		models: []ollama.Model{{Name: "qwen3:8b"}},
	}
	server := NewServer(cfg, fake, nil)

	responses := exchange(t, server, Request{
		Type:      RequestModelList,
		SessionID: "s1",
	})

	if len(responses) != 1 {
		t.Fatalf("len(responses) = %d, want 1", len(responses))
	}
	if responses[0].Type != ResponseModelList {
		t.Fatalf("Type = %q", responses[0].Type)
	}

	var generalInstalled bool
	for _, profile := range responses[0].Profiles {
		if profile.Profile == "general" {
			generalInstalled = profile.Installed
		}
	}
	if !generalInstalled {
		t.Fatal("general profile should be installed")
	}
}

func TestQueryUsesHistoryOnSecondPrompt(t *testing.T) {
	cfg := config.Default()
	fake := &fakeOllama{}
	server := NewServer(cfg, fake, nil)

	_ = exchange(t, server, Request{Type: RequestQuery, SessionID: "s1", Prompt: "first"})
	_ = exchange(t, server, Request{Type: RequestQuery, SessionID: "s1", Prompt: "second"})

	if !strings.Contains(fake.prompt, "Conversation so far:") {
		t.Fatalf("prompt did not include history: %q", fake.prompt)
	}
	if !strings.Contains(fake.prompt, "user: first") {
		t.Fatalf("prompt did not include first user message: %q", fake.prompt)
	}
}

func exchange(t *testing.T, server *Server, req Request) []Response {
	t.Helper()

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	go server.handleConn(context.Background(), serverConn)

	if err := json.NewEncoder(clientConn).Encode(req); err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}

	decoder := json.NewDecoder(clientConn)
	var responses []Response
	for {
		var resp Response
		if err := decoder.Decode(&resp); err != nil {
			t.Fatalf("Decode returned error: %v", err)
		}
		responses = append(responses, resp)
		if resp.Type != ResponseChunk {
			return responses
		}
	}
}
