package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ai-shell/internal/config"
)

func TestNewRequiresURL(t *testing.T) {
	if _, err := New(config.WebConfig{Enabled: true}); err == nil {
		t.Fatal("expected error when searxng_url is empty")
	}
}

func TestSearchParsesResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "format=json") {
			t.Errorf("missing format=json in query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"A","url":"https://a.example","content":"about a"},{"title":"B","url":"","content":"no url"}]}`))
	}))
	defer srv.Close()

	client, err := New(config.WebConfig{SearxngURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}

	results, err := client.Search(context.Background(), "golang news")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1 (url-less entry skipped)", len(results))
	}
	if results[0].URL != "https://a.example" {
		t.Fatalf("URL = %q", results[0].URL)
	}
}

func TestSearchErrorOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client, _ := New(config.WebConfig{SearxngURL: srv.URL})
	if _, err := client.Search(context.Background(), "x"); err == nil {
		t.Fatal("expected error on non-200")
	}
}

func TestParseToolCall(t *testing.T) {
	if q, ok := ParseToolCall(`{"web_search": "latest go release"}`); !ok || q != "latest go release" {
		t.Fatalf("ParseToolCall = %q, %v", q, ok)
	}
	// Prose answer should not be treated as a tool call.
	if _, ok := ParseToolCall("The answer is 42."); ok {
		t.Fatal("plain text should not parse as a tool call")
	}
	// JSON without the web_search key should not be a tool call.
	if _, ok := ParseToolCall(`{"explanation": "hi"}`); ok {
		t.Fatal("JSON without web_search should not parse")
	}
}

func TestFormatLimitsResults(t *testing.T) {
	results := make([]Result, 0, 8)
	for i := 0; i < 8; i++ {
		results = append(results, Result{Title: "t", URL: "https://e/x", Content: "c"})
	}
	out := Format(results)
	if strings.Count(out, "https://e/x") != maxResults {
		t.Fatalf("Format rendered %d results, want %d", strings.Count(out, "https://e/x"), maxResults)
	}
}
