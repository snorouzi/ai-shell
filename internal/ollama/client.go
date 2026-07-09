package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"ai-shell/internal/config"
)

// Client talks directly to Ollama's HTTP API.
type Client struct {
	baseURL       string
	contextLength int
	httpClient    *http.Client
}

// Model is the subset of /api/tags model metadata this project needs.
type Model struct {
	Name       string    `json:"name"`
	Model      string    `json:"model"`
	ModifiedAt time.Time `json:"modified_at"`
	Size       int64     `json:"size"`
}

// GenerateRequest is the payload needed for Ollama's /api/generate endpoint.
type GenerateRequest struct {
	Model  string
	Prompt string
	System string
}

// GenerateChunk is one streaming response object from /api/generate.
type GenerateChunk struct {
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"created_at"`
	Response  string    `json:"response"`
	Done      bool      `json:"done"`
}

type tagsResponse struct {
	Models []Model `json:"models"`
}

type generateHTTPPayload struct {
	Model   string          `json:"model"`
	Prompt  string          `json:"prompt"`
	System  string          `json:"system,omitempty"`
	Stream  bool            `json:"stream"`
	Options generateOptions `json:"options"`
}

type generateOptions struct {
	NumCtx int `json:"num_ctx"`
}

// New builds an Ollama client from config.
func New(cfg config.OllamaConfig) (*Client, error) {
	baseURL, err := normalizeBaseURL(cfg.URL)
	if err != nil {
		return nil, err
	}

	timeout := time.Duration(cfg.RequestTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	return &Client{
		baseURL:       baseURL,
		contextLength: cfg.ContextLength,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

// Tags returns locally pulled Ollama models from GET /api/tags.
func (c *Client) Tags(ctx context.Context) ([]Model, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("/api/tags"), nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, responseError(resp)
	}

	var parsed tagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	return parsed.Models, nil
}

// Generate streams tokens from POST /api/generate. onChunk is called as each
// JSON response object arrives.
func (c *Client) Generate(ctx context.Context, req GenerateRequest, onChunk func(GenerateChunk) error) error {
	if req.Model == "" {
		return fmt.Errorf("ollama generate: model is required")
	}
	if req.Prompt == "" {
		return fmt.Errorf("ollama generate: prompt is required")
	}
	if onChunk == nil {
		return fmt.Errorf("ollama generate: onChunk callback is required")
	}

	payload := generateHTTPPayload{
		Model:  req.Model,
		Prompt: req.Prompt,
		System: req.System,
		Stream: true,
		Options: generateOptions{
			NumCtx: c.contextLength,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("/api/generate"), bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return responseError(resp)
	}

	decoder := json.NewDecoder(resp.Body)
	for {
		var chunk GenerateChunk
		if err := decoder.Decode(&chunk); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if err := onChunk(chunk); err != nil {
			return err
		}
		if chunk.Done {
			return nil
		}
	}
}

// Pull shells out to the Ollama CLI. Ollama's own progress output is streamed
// through stdout/stderr when writers are provided.
func Pull(ctx context.Context, model string, stdout, stderr io.Writer) error {
	if model == "" {
		return fmt.Errorf("ollama pull: model is required")
	}

	cmd := exec.CommandContext(ctx, "ollama", "pull", model)
	if stdout != nil {
		cmd.Stdout = stdout
	} else {
		cmd.Stdout = os.Stdout
	}
	if stderr != nil {
		cmd.Stderr = stderr
	} else {
		cmd.Stderr = os.Stderr
	}
	return cmd.Run()
}

func (c *Client) endpoint(path string) string {
	return c.baseURL + path
}

func normalizeBaseURL(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("ollama url is required")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("ollama url must include scheme and host: %q", raw)
	}

	return strings.TrimRight(raw, "/"), nil
}

func responseError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = resp.Status
	}

	method := "request"
	path := ""
	if resp.Request != nil {
		method = resp.Request.Method
		if resp.Request.URL != nil {
			path = resp.Request.URL.Path
		}
	}
	return fmt.Errorf("ollama %s %s: %s", method, path, message)
}
