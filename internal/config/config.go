package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	appName       = "ai-shell"
	appVersion    = "0.1.0"
	configDirName = "ai-shell"
	configFile    = "config.yaml"
)

// Config mirrors ~/.config/ai-shell/config.yaml.
//
// The yaml tags tell yaml.v3 which YAML key maps to each Go field. That lets
// Go use idiomatic exported field names while preserving the spec's snake_case
// config file.
type Config struct {
	App       AppConfig       `yaml:"app"`
	Ollama    OllamaConfig    `yaml:"ollama"`
	Daemon    DaemonConfig    `yaml:"daemon"`
	Session   SessionConfig   `yaml:"session"`
	Context   ContextConfig   `yaml:"context"`
	Execution ExecutionConfig `yaml:"execution"`
	Web       WebConfig       `yaml:"web"`
	UI        UIConfig        `yaml:"ui"`

	path string `yaml:"-"`
}

type AppConfig struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
}

type OllamaConfig struct {
	URL                   string            `yaml:"url"`
	Models                map[string]string `yaml:"models"`
	DefaultProfile        string            `yaml:"default_profile"`
	ContextLength         int               `yaml:"context_length"`
	RequestTimeoutSeconds int               `yaml:"request_timeout_seconds"`
}

type DaemonConfig struct {
	SocketPath         string `yaml:"socket_path"`
	IdleTimeoutMinutes int    `yaml:"idle_timeout_minutes"`
	LogFile            string `yaml:"log_file"`
}

type SessionConfig struct {
	MaxHistoryMessages int `yaml:"max_history_messages"`
}

type ContextConfig struct {
	IncludeLastCommand bool `yaml:"include_last_command"`
	IncludeExitCode    bool `yaml:"include_exit_code"`
	IncludeCwd         bool `yaml:"include_cwd"`
	IncludeGitStatus   bool `yaml:"include_git_status"`
}

type ExecutionConfig struct {
	Enabled                             bool      `yaml:"enabled"`
	ConfirmBeforeExecute                bool      `yaml:"confirm_before_execute"`
	RequireTypedConfirmationForHighRisk bool      `yaml:"require_typed_confirmation_for_high_risk"`
	RiskRules                           RiskRules `yaml:"risk_rules"`
	LogFile                             string    `yaml:"log_file"`
}

type RiskRules struct {
	High   []string `yaml:"high"`
	Medium []string `yaml:"medium"`
	Low    []string `yaml:"low"`
}

type WebConfig struct {
	Enabled     bool `yaml:"enabled"`
	Provider    string `yaml:"provider"`
	SearxngPort int  `yaml:"searxng_port"`
}

type UIConfig struct {
	RenderMarkdown bool `yaml:"render_markdown"`
}

// Default returns the shipped configuration from the implementation spec.
func Default() Config {
	return Config{
		App: AppConfig{
			Name:    appName,
			Version: appVersion,
		},
		Ollama: OllamaConfig{
			URL: "http://localhost:11434",
			Models: map[string]string{
				"general": "qwen3:8b",
				"coding":  "qwen2.5-coder:7b",
				"fast":    "qwen3:4b",
			},
			DefaultProfile:        "general",
			ContextLength:         4096,
			RequestTimeoutSeconds: 60,
		},
		Daemon: DaemonConfig{
			SocketPath:         "${XDG_RUNTIME_DIR}/ai-shell.sock",
			IdleTimeoutMinutes: 30,
			LogFile:            "~/.local/state/ai-shell/daemon.log",
		},
		Session: SessionConfig{
			MaxHistoryMessages: 20,
		},
		Context: ContextConfig{
			IncludeLastCommand: true,
			IncludeExitCode:    true,
			IncludeCwd:         true,
			IncludeGitStatus:   true,
		},
		Execution: ExecutionConfig{
			Enabled:                             false,
			ConfirmBeforeExecute:                true,
			RequireTypedConfirmationForHighRisk: true,
			RiskRules: RiskRules{
				High: []string{
					"rm -rf*",
					"sudo *",
					"* /etc/*",
					"dd *",
					"mkfs*",
				},
				Medium: []string{
					"pacman -R*",
					"systemctl stop*",
					"git push --force*",
				},
				Low: []string{},
			},
			LogFile: "~/.local/state/ai-shell/executed_commands.log",
		},
		Web: WebConfig{
			Enabled:    true,
			Provider:   "searxng",
			SearxngPort: 8081,
		},
		UI: UIConfig{
			RenderMarkdown: true,
		},
	}
}

// Path returns the default config file location.
func Path() (string, error) {
	configHome, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configHome, configDirName, configFile), nil
}

// Load reads the default config path, returning defaults when the file does not
// exist yet.
func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	return LoadFile(path)
}

// LoadFile reads a config file from path. Missing files return Default().
func LoadFile(path string) (Config, error) {
	cfg := Default()
	cfg.path = path

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg.expandPaths()
			return cfg, nil
		}
		return Config{}, err
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}

	// yaml.v3 merges into the existing (default) Models map rather than
	// replacing it, which would silently re-add profiles a user removed from
	// the file. Detect whether the file explicitly defines ollama.models and,
	// if so, treat the file as authoritative for that map.
	var probe struct {
		Ollama struct {
			Models map[string]string `yaml:"models"`
		} `yaml:"ollama"`
	}
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return Config{}, err
	}
	if probe.Ollama.Models != nil {
		cfg.Ollama.Models = probe.Ollama.Models
	}

	cfg.path = path
	cfg.expandPaths()
	return cfg, nil
}

// Save writes the config to its original path, or the default config path when
// it was not loaded from a file.
func (c Config) Save() error {
	path := c.path
	if path == "" {
		var err error
		path, err = Path()
		if err != nil {
			return err
		}
	}
	return c.SaveFile(path)
}

// SaveFile writes the config to path, creating the parent directory as needed.
func (c Config) SaveFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o644)
}

// FilePath reports the path this config was loaded from, or "" when it came
// from Default() without a backing file.
func (c Config) FilePath() string {
	return c.path
}

// AddProfile persistently adds or updates a single entry under ollama.models in
// the config file at path. It edits the YAML surgically via the node API so all
// other content is preserved verbatim — including templated paths like
// ${XDG_RUNTIME_DIR} and ~, comments, and key ordering. When the file does not
// exist yet it is seeded from Default() (which keeps those path templates). This
// is deliberately used instead of Config.Save() so the daemon never rewrites
// unrelated, already-expanded fields from its in-memory config.
func AddProfile(path, profile, model string) error {
	profile = strings.TrimSpace(profile)
	model = strings.TrimSpace(model)
	if profile == "" {
		return errors.New("profile name is required")
	}
	if model == "" {
		return errors.New("model tag is required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if data, err = yaml.Marshal(Default()); err != nil {
			return err
		}
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}

	root := documentRoot(&doc)
	ollama := ensureMapping(root, "ollama")
	models := ensureMapping(ollama, "models")
	setScalar(models, profile, model)

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// RemoveProfile persistently deletes an entry from ollama.models in the config
// file at path, editing the YAML surgically so all other content is preserved.
// It returns an error when the file, the ollama.models mapping, or the profile
// does not exist.
func RemoveProfile(path, profile string) error {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return errors.New("profile name is required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("profile %q not found", profile)
		}
		return err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}

	root := documentRoot(&doc)
	ollama := mappingValue(root, "ollama")
	if ollama == nil {
		return fmt.Errorf("profile %q not found", profile)
	}
	models := mappingValue(ollama, "models")
	if models == nil || models.Kind != yaml.MappingNode || !deleteKey(models, profile) {
		return fmt.Errorf("profile %q not found", profile)
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// documentRoot returns the top-level mapping node of a parsed YAML document,
// initialising an empty mapping when the document is blank.
func documentRoot(doc *yaml.Node) *yaml.Node {
	if doc.Kind == 0 {
		doc.Kind = yaml.DocumentNode
	}
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
			doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
		}
		return doc.Content[0]
	}
	return doc
}

// mappingValue returns the value node stored under key in a mapping node, or nil.
func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// ensureMapping returns the mapping value stored under key in parent, creating
// (or coercing) it to an empty mapping when absent or not already a mapping.
func ensureMapping(parent *yaml.Node, key string) *yaml.Node {
	if v := mappingValue(parent, key); v != nil {
		if v.Kind != yaml.MappingNode {
			v.Kind = yaml.MappingNode
			v.Tag = "!!map"
			v.Value = ""
			v.Content = nil
		}
		return v
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	valNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	parent.Content = append(parent.Content, keyNode, valNode)
	return valNode
}

// setScalar sets key to a string value in a mapping node, updating the existing
// entry in place when present or appending a new one otherwise.
func setScalar(mapping *yaml.Node, key, value string) {
	if v := mappingValue(mapping, key); v != nil {
		v.Kind = yaml.ScalarNode
		v.Tag = "!!str"
		v.Value = value
		v.Content = nil
		return
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	valNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
	mapping.Content = append(mapping.Content, keyNode, valNode)
}

// deleteKey removes the key/value pair stored under key from a mapping node,
// reporting whether an entry was actually removed.
func deleteKey(mapping *yaml.Node, key string) bool {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return true
		}
	}
	return false
}

func (c *Config) expandPaths() {
	c.Daemon.SocketPath = expandPath(c.Daemon.SocketPath)
	c.Daemon.LogFile = expandPath(c.Daemon.LogFile)
	c.Execution.LogFile = expandPath(c.Execution.LogFile)
}

func expandPath(path string) string {
	if path == "" {
		return path
	}

	path = os.ExpandEnv(path)
	if path == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			return home
		}
		return path
	}
	if len(path) > 2 && path[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
