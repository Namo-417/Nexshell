package ai_test

import (
	"bytes"
	"strings"
	"testing"

	"nexshell/internal/ai"
	"nexshell/internal/registry"
)

func TestRiskAnalysis(t *testing.T) {
	tests := []struct {
		cmd       string
		wantLevel ai.RiskLevel
		wantAck   bool
	}{
		{"ls -la", ai.RiskLow, false},
		{"processes | where cpu > 50", ai.RiskLow, false},
		{"kill 4521", ai.RiskMedium, false},
		{"git reset --hard", ai.RiskHigh, true},
		{"rm -rf /", ai.RiskCritical, true},
		{"format C:", ai.RiskCritical, true},
	}

	for _, tt := range tests {
		got := ai.AnalyzeRisk(tt.cmd)
		if got.Level != tt.wantLevel {
			t.Errorf("AnalyzeRisk(%q).Level = %v, want %v", tt.cmd, got.Level, tt.wantLevel)
		}
		if got.RequiresAck != tt.wantAck {
			t.Errorf("AnalyzeRisk(%q).RequiresAck = %v, want %v", tt.cmd, got.RequiresAck, tt.wantAck)
		}
	}
}

func TestDiagnoseQuery_PortBind(t *testing.T) {
	cand := ai.DiagnoseQuery("why is my server failing to bind?")
	if cand.Command == "" {
		t.Fatal("expected candidate command")
	}
	if !strings.Contains(strings.ToLower(cand.Explanation), "port") && !strings.Contains(strings.ToLower(cand.Explanation), "bind") {
		t.Fatalf("expected port/bind in explanation, got: %s", cand.Explanation)
	}
}

func TestGenerateCommand(t *testing.T) {
	cand := ai.GenerateCommand("show processes with high cpu")
	if !strings.Contains(cand.Command, "processes") {
		t.Fatalf("expected processes in generated command, got: %s", cand.Command)
	}
}

func TestExplainCmd_Execution(t *testing.T) {
	cmd := &ai.ExplainCmd{}
	var out bytes.Buffer
	// Provide 'n' as confirmation answer
	in := strings.NewReader("n\n")

	ctx := registry.Context{
		Stdin:  in,
		Stdout: &out,
	}

	if err := cmd.Run(ctx, []string{"why", "is", "my", "server", "failing", "to", "bind?"}); err != nil {
		t.Fatalf("explain run failed: %v", err)
	}

	res := out.String()
	if !strings.Contains(res, "Diagnosis") || !strings.Contains(res, "Recommended Command") {
		t.Fatalf("expected diagnosis output, got: %s", res)
	}
}
