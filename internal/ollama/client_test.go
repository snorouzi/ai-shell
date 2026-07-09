package ollama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"ai-shell/internal/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestTags(t *testing.T) {
	cfg := config.Default().Ollama
	cfg.URL = "http://ollama.test"

	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", req.Method)
		}
		if req.URL.Path != "/api/tags" {
			t.Fatalf("path = %s, want /api/tags", req.URL.Path)
		}

		return response(http.StatusOK, `{"models":[{"name":"qwen3:4b","model":"qwen3:4b","size":3000}]}`), nil
	})

	models, err := client.Tags(context.Background())
	if err != nil {
		t.Fatalf("Tags returned error: %v", err)
	}

	if len(models) != 1 {
		t.Fatalf("len(models) = %d, want 1", len(models))
	}
	if models[0].Name != "qwen3:4b" {
		t.Fatalf("models[0].Name = %q, want qwen3:4b", models[0].Name)
	}
}

func TestGenerateStreamsChunksAndSendsNumCtx(t *testing.T) {
	var gotPayload map[string]any

	cfg := config.Default().Ollama
	cfg.URL = "http://ollama.test"
	cfg.ContextLength = 8192

	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", req.Method)
		}
		if req.URL.Path != "/api/generate" {
			t.Fatalf("path = %s, want /api/generate", req.URL.Path)
		}
		if err := json.NewDecoder(req.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("Decode request body returned error: %v", err)
		}

		body := `{"response":"hello ","done":false}` + "\n" +
			`{"response":"world","done":false}` + "\n" +
			`{"done":true}` + "\n"
		return response(http.StatusOK, body), nil
	})

	var chunks []GenerateChunk
	err = client.Generate(context.Background(), GenerateRequest{
		Model:  "qwen3:4b",
		Prompt: "say hello",
		System: "be concise",
	}, func(chunk GenerateChunk) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	responses := []string{chunks[0].Response, chunks[1].Response}
	if !reflect.DeepEqual(responses, []string{"hello ", "world"}) {
		t.Fatalf("responses = %#v", responses)
	}

	if gotPayload["stream"] != true {
		t.Fatalf("stream = %#v, want true", gotPayload["stream"])
	}
	options, ok := gotPayload["options"].(map[string]any)
	if !ok {
		t.Fatalf("options = %#v, want object", gotPayload["options"])
	}
	if options["num_ctx"] != float64(8192) {
		t.Fatalf("num_ctx = %#v, want 8192", options["num_ctx"])
	}
}

func TestGenerateReturnsHTTPError(t *testing.T) {
	cfg := config.Default().Ollama
	cfg.URL = "http://ollama.test"

	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return response(http.StatusServiceUnavailable, "ollama unavailable"), nil
	})

	err = client.Generate(context.Background(), GenerateRequest{
		Model:  "qwen3:4b",
		Prompt: "hello",
	}, func(chunk GenerateChunk) error {
		return nil
	})
	if err == nil {
		t.Fatal("Generate returned nil error")
	}
}

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}
