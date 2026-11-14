package system_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"nexshell/internal/registry"
	"nexshell/internal/system"
)

func TestSystemCmd(t *testing.T) {
	cmd := &system.SystemCmd{}
	var out bytes.Buffer
	ctx := registry.Context{Stdout: &out}

	if err := cmd.Run(ctx, nil); err != nil {
		t.Fatalf("system cmd failed: %v", err)
	}

	res := out.String()
	if !strings.Contains(res, "Go Runtime") || !strings.Contains(res, "Logical CPUs") {
		t.Fatalf("unexpected system output: %s", res)
	}
}

func TestAuditLoggerAndCmd(t *testing.T) {
	logger := &system.AuditLogger{}
	logger.Log(system.AuditRecord{
		Timestamp: time.Now(),
		User:      "testuser",
		CWD:       "/test",
		Command:   "processes | where cpu > 50",
		Duration:  50 * time.Millisecond,
		ExitCode:  0,
		RiskLevel: "LOW",
	})

	records := logger.Recent(10)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	cmd := system.NewAuditCmd(logger)
	var out bytes.Buffer
	ctx := registry.Context{Stdout: &out}
	if err := cmd.Run(ctx, nil); err != nil {
		t.Fatalf("audit command failed: %v", err)
	}

	if !strings.Contains(out.String(), "processes") {
		t.Fatalf("expected command in audit output: %s", out.String())
	}
}

func TestBenchmarksCmd(t *testing.T) {
	cmd := &system.BenchmarksCmd{}
	var out bytes.Buffer
	ctx := registry.Context{Stdout: &out}

	if err := cmd.Run(ctx, nil); err != nil {
		t.Fatalf("benchmarks cmd failed: %v", err)
	}

	res := out.String()
	if !strings.Contains(res, "Memory Footprint") || !strings.Contains(res, "Pipeline Throughput") {
		t.Fatalf("unexpected benchmarks output: %s", res)
	}
}
