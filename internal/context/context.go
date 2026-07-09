package shellcontext

import (
	stdctx "context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"ai-shell/internal/config"
)

const (
	EnvLastCommand = "AI_SHELL_LAST_COMMAND"
	EnvExitCode    = "AI_SHELL_EXIT_CODE"
)

type Payload struct {
	CWD         string   `json:"cwd,omitempty"`
	LastCommand string   `json:"last_command,omitempty"`
	ExitCode    *int     `json:"exit_code,omitempty"`
	Git         *GitInfo `json:"git,omitempty"`
}

type GitInfo struct {
	Branch string `json:"branch,omitempty"`
	Status string `json:"status,omitempty"`
}

func GatherClient(cfg config.ContextConfig) Payload {
	var payload Payload

	if cfg.IncludeCwd {
		if cwd, err := os.Getwd(); err == nil {
			payload.CWD = cwd
		}
	}
	if cfg.IncludeLastCommand {
		payload.LastCommand = os.Getenv(EnvLastCommand)
	}
	if cfg.IncludeExitCode {
		if value := os.Getenv(EnvExitCode); value != "" {
			if exitCode, err := strconv.Atoi(value); err == nil {
				payload.ExitCode = &exitCode
			}
		}
	}

	return payload
}

func Enrich(ctx stdctx.Context, cfg config.ContextConfig, payload Payload) Payload {
	if cfg.IncludeGitStatus && payload.CWD != "" {
		if git := gatherGit(ctx, payload.CWD); git != nil {
			payload.Git = git
		}
	}
	return payload
}

func SystemPrompt(cfg config.ContextConfig, payload Payload) string {
	var lines []string

	if cfg.IncludeCwd && payload.CWD != "" {
		lines = append(lines, fmt.Sprintf("cwd: %s", payload.CWD))
	}
	if cfg.IncludeExitCode && payload.ExitCode != nil {
		lines = append(lines, fmt.Sprintf("last_exit_code: %d", *payload.ExitCode))
	}
	if cfg.IncludeLastCommand && payload.LastCommand != "" {
		lines = append(lines, fmt.Sprintf("last_command: %s", payload.LastCommand))
	}
	if cfg.IncludeGitStatus && payload.Git != nil {
		if payload.Git.Branch != "" {
			lines = append(lines, fmt.Sprintf("git_branch: %s", payload.Git.Branch))
		}
		if payload.Git.Status != "" {
			lines = append(lines, "git_status:")
			lines = append(lines, payload.Git.Status)
		}
	}

	if len(lines) == 0 {
		return ""
	}

	return "Shell context:\n" + strings.Join(lines, "\n")
}

func gatherGit(ctx stdctx.Context, cwd string) *GitInfo {
	if strings.TrimSpace(gitOutput(ctx, cwd, "rev-parse", "--is-inside-work-tree")) != "true" {
		return nil
	}

	return &GitInfo{
		Branch: strings.TrimSpace(gitOutput(ctx, cwd, "branch", "--show-current")),
		Status: strings.TrimSpace(gitOutput(ctx, cwd, "status", "--porcelain")),
	}
}

func gitOutput(ctx stdctx.Context, cwd string, args ...string) string {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", cwd}, args...)...)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(output)
}
