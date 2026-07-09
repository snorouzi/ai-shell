package shellcontext

import (
	"strings"
	"testing"

	"ai-shell/internal/config"
)

func TestGatherClientReadsEnabledValues(t *testing.T) {
	t.Setenv(EnvLastCommand, "go test ./...")
	t.Setenv(EnvExitCode, "2")

	payload := GatherClient(config.ContextConfig{
		IncludeCwd:         true,
		IncludeLastCommand: true,
		IncludeExitCode:    true,
	})

	if payload.CWD == "" {
		t.Fatal("CWD should be set")
	}
	if payload.LastCommand != "go test ./..." {
		t.Fatalf("LastCommand = %q", payload.LastCommand)
	}
	if payload.ExitCode == nil || *payload.ExitCode != 2 {
		t.Fatalf("ExitCode = %#v", payload.ExitCode)
	}
}

func TestSystemPromptUsesEnabledContext(t *testing.T) {
	exitCode := 1
	prompt := SystemPrompt(config.ContextConfig{
		IncludeCwd:         true,
		IncludeLastCommand: true,
		IncludeExitCode:    true,
		IncludeGitStatus:   true,
	}, Payload{
		CWD:         "/tmp/project",
		LastCommand: "make test",
		ExitCode:    &exitCode,
		Git: &GitInfo{
			Branch: "main",
			Status: " M main.go",
		},
	})

	for _, want := range []string{
		"Shell context:",
		"cwd: /tmp/project",
		"last_exit_code: 1",
		"last_command: make test",
		"git_branch: main",
		"git_status:\n M main.go",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt %q does not contain %q", prompt, want)
		}
	}
}

func TestSystemPromptOmitsDisabledContext(t *testing.T) {
	exitCode := 1
	prompt := SystemPrompt(config.ContextConfig{}, Payload{
		CWD:         "/tmp/project",
		LastCommand: "make test",
		ExitCode:    &exitCode,
		Git:         &GitInfo{Branch: "main"},
	})

	if prompt != "" {
		t.Fatalf("prompt = %q, want empty", prompt)
	}
}
