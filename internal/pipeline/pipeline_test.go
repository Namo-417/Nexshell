package pipeline_test

import (
	"bytes"
	"strings"
	"testing"

	"nexshell/internal/pipeline"
	"nexshell/internal/registry"
)

func TestPipeline_StructuredFlow(t *testing.T) {
	inputJSON := `[
		{"pid": 100, "name": "system", "cpu": 5.2, "memory": 200},
		{"pid": 200, "name": "webserver", "cpu": 65.4, "memory": 500},
		{"pid": 300, "name": "worker", "cpu": 12.0, "memory": 150},
		{"pid": 400, "name": "miner", "cpu": 95.1, "memory": 800}
	]`

	// 1. Where: where cpu > 50
	whereCmd := &pipeline.WhereCmd{}
	var whereOut bytes.Buffer
	ctx := registry.Context{
		Stdin:  strings.NewReader(inputJSON),
		Stdout: &whereOut,
	}
	if err := whereCmd.Run(ctx, []string{"cpu", ">", "50"}); err != nil {
		t.Fatalf("where failed: %v", err)
	}

	whereResult := whereOut.String()
	if !strings.Contains(whereResult, "webserver") || !strings.Contains(whereResult, "miner") {
		t.Fatalf("expected webserver and miner, got: %s", whereResult)
	}
	if strings.Contains(whereResult, "worker") || strings.Contains(whereResult, "system") {
		t.Fatalf("expected system and worker filtered out, got: %s", whereResult)
	}

	// 2. Select: select pid,name,cpu
	selectCmd := &pipeline.SelectCmd{}
	var selectOut bytes.Buffer
	ctxSelect := registry.Context{
		Stdin:  strings.NewReader(whereResult),
		Stdout: &selectOut,
	}
	if err := selectCmd.Run(ctxSelect, []string{"pid,name,cpu"}); err != nil {
		t.Fatalf("select failed: %v", err)
	}

	selectResult := selectOut.String()
	if strings.Contains(selectResult, "memory") {
		t.Fatalf("expected memory column to be excluded, got: %s", selectResult)
	}
	if !strings.Contains(selectResult, "webserver") {
		t.Fatalf("expected webserver in result, got: %s", selectResult)
	}

	// 3. Sort: sort --desc cpu
	sortCmd := &pipeline.SortCmd{}
	var sortOut bytes.Buffer
	ctxSort := registry.Context{
		Stdin:  strings.NewReader(selectResult),
		Stdout: &sortOut,
	}
	if err := sortCmd.Run(ctxSort, []string{"--desc", "cpu"}); err != nil {
		t.Fatalf("sort failed: %v", err)
	}

	// 4. JSON: json
	jsonCmd := &pipeline.JSONCmd{}
	var jsonOut bytes.Buffer
	ctxJSON := registry.Context{
		Stdin:  strings.NewReader(sortOut.String()),
		Stdout: &jsonOut,
	}
	if err := jsonCmd.Run(ctxJSON, nil); err != nil {
		t.Fatalf("json failed: %v", err)
	}

	jsonStr := jsonOut.String()
	if !strings.HasPrefix(strings.TrimSpace(jsonStr), "[") {
		t.Fatalf("expected JSON array, got: %s", jsonStr)
	}
}

func TestPipeline_TextSort(t *testing.T) {
	text := "banana\napple\ncherry\n"
	sortCmd := &pipeline.SortCmd{}
	var out bytes.Buffer
	ctx := registry.Context{
		Stdin:  strings.NewReader(text),
		Stdout: &out,
	}
	if err := sortCmd.Run(ctx, nil); err != nil {
		t.Fatalf("text sort failed: %v", err)
	}
	got := strings.TrimSpace(out.String())
	want := "apple\nbanana\ncherry"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestPipeline_TableInputToWhereAndSelect(t *testing.T) {
	tableInput := `PID    PPID   NAME        CPU    MEMORY  STATUS
─────  ─────  ──────────  ─────  ──────  ───────
100    1      system      5.20   200.00  Running
200    100    webserver   65.40  500.00  Running
300    100    worker      12.00  150.00  Sleeping
400    100    miner       95.10  800.00  Running
`

	whereCmd := &pipeline.WhereCmd{}
	var whereOut bytes.Buffer
	ctx := registry.Context{
		Stdin:  strings.NewReader(tableInput),
		Stdout: &whereOut,
	}

	if err := whereCmd.Run(ctx, []string{"cpu", ">", "50"}); err != nil {
		t.Fatalf("where failed on table input: %v", err)
	}

	res := whereOut.String()
	if !strings.Contains(res, "webserver") || !strings.Contains(res, "miner") {
		t.Fatalf("expected webserver and miner, got: %s", res)
	}
	if strings.Contains(res, "worker") || strings.Contains(res, "system") {
		t.Fatalf("expected system and worker filtered out, got: %s", res)
	}

	// Now pipe into select: select pid,name,cpu
	selectCmd := &pipeline.SelectCmd{}
	var selectOut bytes.Buffer
	ctxSelect := registry.Context{
		Stdin:  strings.NewReader(res),
		Stdout: &selectOut,
	}

	if err := selectCmd.Run(ctxSelect, []string{"pid,name,cpu"}); err != nil {
		t.Fatalf("select failed: %v", err)
	}

	selRes := selectOut.String()
	if strings.Contains(selRes, "memory") || strings.Contains(selRes, "status") {
		t.Fatalf("unexpected columns in select result: %s", selRes)
	}
}
