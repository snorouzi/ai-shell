package execution

import (
	"strings"
	"testing"

	"ai-shell/internal/config"
)

func TestClassifyEscalatesToHighestRuleTier(t *testing.T) {
	rules := config.RiskRules{
		High:   []string{"sudo *", "rm -rf*"},
		Medium: []string{"systemctl stop*"},
		Low:    []string{"ls *"},
	}

	if got := Classify([]string{"ls -la"}, "", rules); got != RiskLow {
		t.Fatalf("ls => %q, want low", got)
	}
	if got := Classify([]string{"systemctl stop cups"}, "", rules); got != RiskMedium {
		t.Fatalf("systemctl => %q, want medium", got)
	}
	if got := Classify([]string{"sudo reboot"}, "", rules); got != RiskHigh {
		t.Fatalf("sudo => %q, want high", got)
	}
}

func TestClassifyMatchesSlashAndSpace(t *testing.T) {
	rules := config.RiskRules{High: []string{"rm -rf*"}}
	if got := Classify([]string{"rm -rf /etc/passwd"}, "", rules); got != RiskHigh {
		t.Fatalf("rm -rf /etc/passwd => %q, want high", got)
	}
}

func TestClassifyCombinesModelRisk(t *testing.T) {
	rules := config.RiskRules{}
	// Model claims high but no rule matches; result should still be high.
	if got := Classify([]string{"echo hi"}, RiskHigh, rules); got != RiskHigh {
		t.Fatalf("model high => %q, want high", got)
	}
}

func TestConfirmDefaultsToNo(t *testing.T) {
	got, err := Confirm(strings.NewReader(""), &strings.Builder{}, Suggestion{Commands: []string{"ls"}}, config.ExecutionConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("empty input should default to not confirmed")
	}
}

func TestConfirmHighRiskRequiresTypedYes(t *testing.T) {
	cfg := config.ExecutionConfig{RequireTypedConfirmationForHighRisk: true}
	got, err := Confirm(strings.NewReader("y\n"), &strings.Builder{}, Suggestion{Risk: RiskHigh, Commands: []string{"sudo reboot"}}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("[y/N] answer must NOT confirm a high-risk command")
	}

	got, err = Confirm(strings.NewReader("yes\n"), &strings.Builder{}, Suggestion{Risk: RiskHigh, Commands: []string{"sudo reboot"}}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("typed yes must confirm a high-risk command")
	}
}
