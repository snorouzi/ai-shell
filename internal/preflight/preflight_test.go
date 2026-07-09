package preflight

import (
	"strings"
	"testing"

	"ai-shell/internal/config"
)

func TestPromptBoolDefaults(t *testing.T) {
	if !promptBool(strings.NewReader(""), &strings.Builder{}, "q? ", true) {
		t.Fatal("expected default true when input is empty")
	}
	if promptBool(strings.NewReader(""), &strings.Builder{}, "q? ", false) {
		t.Fatal("expected default false when input is empty")
	}
}

func TestPromptBoolParsesInput(t *testing.T) {
	cases := map[string]bool{
		"y\n":    true,
		"yes\n":  true,
		"n\n":    false,
		"no\n":   false,
		"maybe\n": false,
	}
	for in, want := range cases {
		got := promptBool(strings.NewReader(in), &strings.Builder{}, "q? ", false)
		if got != want {
			t.Fatalf("promptBool(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSuggestContextLengthTiers(t *testing.T) {
	buf := &strings.Builder{}
	suggestContextLength(4, buf)
	if !strings.Contains(buf.String(), "4096") {
		t.Fatalf("vram 4 should suggest 4096, got %q", buf.String())
	}

	buf.Reset()
	suggestContextLength(10, buf)
	if !strings.Contains(buf.String(), "8192") {
		t.Fatalf("vram 10 should suggest 8192, got %q", buf.String())
	}

	buf.Reset()
	suggestContextLength(32, buf)
	if !strings.Contains(buf.String(), "16384") {
		t.Fatalf("vram 32 should suggest 16384, got %q", buf.String())
	}
}

func TestPrintHeadroomMessages(t *testing.T) {
	cfg := config.Default()

	buf := &strings.Builder{}
	printHeadroom(32, cfg, buf)
	if !strings.Contains(buf.String(), "upgrade to larger models") {
		t.Fatalf("high VRAM should suggest upgrade, got %q", buf.String())
	}

	buf.Reset()
	printHeadroom(6, cfg, buf)
	if !strings.Contains(buf.String(), "may struggle with the defaults") {
		t.Fatalf("low VRAM should warn, got %q", buf.String())
	}
}
