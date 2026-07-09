package execution

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"ai-shell/internal/config"
)

// Risk tiers, ordered low → high.
const (
	RiskLow    = "low"
	RiskMedium = "medium"
	RiskHigh   = "high"
)

// Suggestion is the structured answer the model returns when execution is
// relevant. It is also what the daemon keeps as the pending suggestion between
// the suggestion response and the user's confirm.
type Suggestion struct {
	Explanation string   `json:"explanation"`
	Commands    []string `json:"commands"`
	Risk        string   `json:"risk"`
}

// SystemPrompt instructs the model to reply with a single JSON object so the
// daemon can parse a structured command suggestion instead of prose.
func SystemPrompt() string {
	return "You are a shell assistant. Respond with a single JSON object and nothing else: " +
		`{"explanation": "...", "commands": ["..."], "risk": "low|medium|high"}. ` +
		"If no shell command is appropriate, use an empty commands array."
}

func riskRank(r string) int {
	switch strings.ToLower(strings.TrimSpace(r)) {
	case RiskHigh:
		return 3
	case RiskMedium:
		return 2
	case RiskLow:
		return 1
	default:
		return 0
	}
}

func riskName(rank int) string {
	switch rank {
	case 3:
		return RiskHigh
	case 2:
		return RiskMedium
	case 1:
		return RiskLow
	default:
		return ""
	}
}

// Classify returns the highest risk tier implied by the commands against the
// configured glob rules, combined with the model's own claimed risk so the
// rules can confirm (and escalate) what the model reported.
func Classify(commands []string, modelRisk string, rules config.RiskRules) string {
	rank := riskRank(modelRisk)
	for _, cmd := range commands {
		rank = max(rank, riskRank(matchTier(cmd, rules)))
	}
	if rank == 0 {
		return RiskLow
	}
	return riskName(rank)
}

func matchTier(cmd string, rules config.RiskRules) string {
	if anyMatch(cmd, rules.High) {
		return RiskHigh
	}
	if anyMatch(cmd, rules.Medium) {
		return RiskMedium
	}
	if anyMatch(cmd, rules.Low) {
		return RiskLow
	}
	return ""
}

// anyMatch reports whether cmd matches any shell-style glob in patterns.
func anyMatch(cmd string, patterns []string) bool {
	lower := strings.ToLower(cmd)
	for _, p := range patterns {
		if p == "" {
			continue
		}
		re, err := compileGlob(p)
		if err != nil {
			continue
		}
		if re.MatchString(lower) {
			return true
		}
	}
	return false
}

// compileGlob turns a shell-style glob (where * matches any characters,
// including spaces and slashes) into an anchored, case-insensitive regexp.
// This matches the intuitive meaning of the config rules (e.g. "rm -rf*"
// matches "rm -rf /tmp/x") better than path.Match, which stops * at "/".
func compileGlob(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		case '.', '+', '(', ')', '|', '\\', '[', ']', '{', '}', '^', '$':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteString("$")
	return regexp.Compile("(?i)" + b.String())
}

// Confirm prompts the user to approve running the suggested commands. High-risk
// suggestions require the literal word "yes" when
// require_typed_confirmation_for_high_risk is set; everything else uses [y/N].
func Confirm(in io.Reader, out io.Writer, sug Suggestion, cfg config.ExecutionConfig) (bool, error) {
	fmt.Fprintln(out, sug.Explanation)
	for i, cmd := range sug.Commands {
		fmt.Fprintf(out, "  %d. %s\n", i+1, cmd)
	}
	fmt.Fprintf(out, "Risk: %s\n", sug.Risk)

	if sug.Risk == RiskHigh && cfg.RequireTypedConfirmationForHighRisk {
		fmt.Fprint(out, `Type "yes" to confirm, or anything else to cancel: `)
		line, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && line == "" {
			return false, nil
		}
		return strings.TrimSpace(line) == "yes", nil
	}

	fmt.Fprint(out, "Run these commands? [y/N] ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// Execute runs a single command through the shell and appends an audit line to
// execution.log_file. It returns the combined output and the run error (which
// is non-nil for a non-zero exit, after logging).
func Execute(ctx context.Context, cmd, risk string, cfg config.ExecutionConfig) (string, error) {
	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	out, err := c.CombinedOutput()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	_ = appendLog(cfg.LogFile, cmd, risk, exitCode)
	return string(out), err
}

func appendLog(path, cmd, risk string, exitCode int) error {
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	ts := time.Now().Format(time.RFC3339)
	_, err = fmt.Fprintf(f, "%s | risk=%s | exit=%d | %s\n", ts, risk, exitCode, cmd)
	return err
}
