package task_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"nexshell/internal/task"
)

func TestRunDAG_CircularDependency(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, `
tasks:
  a:
    depends: [b]
    commands: ["echo a"]
  b:
    depends: [a]
    commands: ["echo b"]
`)
	ctx := shellContext(t, dir, context.Background())
	err := task.RunDAG(ctx, "a", false)
	if err == nil || !strings.Contains(err.Error(), "circular dependency") {
		t.Fatalf("expected circular dependency error, got: %v", err)
	}
}

func TestRunDAG_TopologicalExecution(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, `
tasks:
  prep:
    commands: ["echo prep"]
  build:
    depends: [prep]
    commands: ["echo build"]
  deploy:
    depends: [build]
    commands: ["echo deploy"]
`)
	var out bytes.Buffer
	ctx := shellContext(t, dir, context.Background())
	ctx.Stdout = &out

	if err := task.RunDAG(ctx, "deploy", false); err != nil {
		t.Fatalf("RunDAG failed: %v", err)
	}

	result := out.String()
	prepIdx := strings.Index(result, "prep")
	buildIdx := strings.Index(result, "build")
	deployIdx := strings.Index(result, "deploy")

	if prepIdx == -1 || buildIdx == -1 || deployIdx == -1 {
		t.Fatalf("missing stage in output: %s", result)
	}
	if !(prepIdx < buildIdx && buildIdx < deployIdx) {
		t.Fatalf("expected prep < build < deploy order, got: %s", result)
	}
}

func TestCommand_ParallelFlag(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, `
tasks:
  test:
    description: "run tests in parallel"
    commands:
      - echo test1
      - echo test2
`)
	var out bytes.Buffer
	ctx := shellContext(t, dir, context.Background())
	ctx.Stdout = &out

	cmd := &task.Command{}
	if err := cmd.Run(ctx, []string{"test", "--parallel"}); err != nil {
		t.Fatalf("task --parallel failed: %v", err)
	}

	res := out.String()
	if !strings.Contains(res, "test1") || !strings.Contains(res, "test2") {
		t.Fatalf("expected test1 and test2 in output, got: %s", res)
	}
}

func TestCommand_List(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, `
project: testproj
tasks:
  build:
    description: "compile binary"
    commands: ["echo ok"]
  test:
    description: "run suite"
    commands: ["echo ok"]
`)
	var out bytes.Buffer
	ctx := shellContext(t, dir, context.Background())
	ctx.Stdout = &out

	cmd := &task.Command{}
	if err := cmd.Run(ctx, []string{"list"}); err != nil {
		t.Fatalf("task list failed: %v", err)
	}

	res := out.String()
	if !strings.Contains(res, "testproj") || !strings.Contains(res, "build") || !strings.Contains(res, "test") {
		t.Fatalf("unexpected list output: %s", res)
	}
}
