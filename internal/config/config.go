package config

import (
	"errors"
	"os"
	"path/filepath"

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
	Enabled    bool   `yaml:"enabled"`
	Provider   string `yaml:"provider"`
	SearxngURL string `yaml:"searxng_url"`
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
			SearxngURL: "http://localhost:8080",
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
