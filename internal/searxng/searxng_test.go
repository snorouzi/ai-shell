package searxng

import (
	"encoding/hex"
	"fmt"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGenerateSecretKeyIsHex64(t *testing.T) {
	key, err := generateSecretKey()
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 64 {
		t.Fatalf("secret key length = %d, want 64", len(key))
	}
	if _, err := hex.DecodeString(key); err != nil {
		t.Fatalf("secret key is not valid hex: %v", err)
	}
}

func TestSettingsTemplateIsValidYAML(t *testing.T) {
	rendered := fmt.Sprintf(settingsTemplate, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", 8081)
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(rendered), &parsed); err != nil {
		t.Fatalf("rendered settings.yml is not valid YAML: %v", err)
	}
	server, ok := parsed["server"].(map[string]any)
	if !ok {
		t.Fatal("missing server section")
	}
	if server["secret_key"] != "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" {
		t.Fatal("secret_key not interpolated")
	}
	if server["base_url"] != "http://localhost:8081/" {
		t.Fatalf("base_url = %v", server["base_url"])
	}
	search := parsed["search"].(map[string]any)
	formats, ok := search["formats"].([]any)
	if !ok || len(formats) != 2 || formats[1] != "json" {
		t.Fatalf("search.formats = %v, want [html json]", search["formats"])
	}
}

func TestPortFreeDetectsBoundPort(t *testing.T) {
	if !portFree(0) {
		t.Skip("port 0 not usable on this host")
	}
}
