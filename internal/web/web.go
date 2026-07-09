package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ai-shell/internal/config"
)

// Result is one SearXNG hit.
type Result struct {
	Title   string
	URL     string
	Content string
}

// Client talks to a self-hosted SearXNG instance's JSON search API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// New builds a SearXNG client. It only makes sense when web.search is enabled
// and a URL is configured.
func New(cfg config.WebConfig) (*Client, error) {
	if cfg.SearxngURL == "" {
		return nil, fmt.Errorf("web.searxng_url is required")
	}
	return &Client{
		baseURL:    strings.TrimRight(cfg.SearxngURL, "/"),
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}, nil
}

type searchResponse struct {
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	} `json:"results"`
}

// Search queries SearXNG and returns the hits. The caller decides how many to
// use; Format limits the rendered output.
func (c *Client) Search(ctx context.Context, query string) ([]Result, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("web search query is empty")
	}

	endpoint := c.baseURL + "/search?format=json&q=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	// SearXNG's JSON endpoint rejects requests without a browser-like UA.
	req.Header.Set("User-Agent", "ai-shell/0.1")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("searxng: %s", resp.Status)
	}

	var parsed searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	results := make([]Result, 0, len(parsed.Results))
	for _, r := range parsed.Results {
		if r.URL == "" {
			continue
		}
		results = append(results, Result{Title: r.Title, URL: r.URL, Content: r.Content})
	}
	return results, nil
}

const maxResults = 5

// Format renders search results as context text the model can read.
func Format(results []Result) string {
	if len(results) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Web search results:\n")
	n := len(results)
	if n > maxResults {
		n = maxResults
	}
	for i := 0; i < n; i++ {
		r := results[i]
		fmt.Fprintf(&b, "[%d] %s\n    %s\n", i+1, r.Title, r.URL)
		if r.Content != "" {
			fmt.Fprintf(&b, "    %s\n", r.Content)
		}
	}
	return b.String()
}

// ParseToolCall extracts a web_search query the model requested. It returns
// ("", false) when the model answered directly instead of calling the tool.
func ParseToolCall(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if start, end := strings.Index(trimmed, "{"), strings.LastIndex(trimmed, "}"); start >= 0 && end > start {
		trimmed = trimmed[start : end+1]
	}
	var call struct {
		WebSearch string `json:"web_search"`
	}
	if err := json.Unmarshal([]byte(trimmed), &call); err != nil {
		return "", false
	}
	if strings.TrimSpace(call.WebSearch) == "" {
		return "", false
	}
	return call.WebSearch, true
}
