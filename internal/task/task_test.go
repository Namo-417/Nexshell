package task_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nexshell/internal/alias"
	"nexshell/internal/builtins"
	"nexshell/internal/executor"
	"nexshell/internal/history"
	"nexshell/internal/registry"
	"nexshell/internal/task"
)

func writeYAML(t *testing.T, dir, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "nex.yaml"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func shellContext(t *testing.T, dir string, ctx context.Context) registry.Context {
	t.Helper()
	task.SetExecute(executor.Execute)
	reg := registry.New()
	builtins.RegisterAll(reg)
	return registry.Context{
		Context:  ctx,
		Stdout:   &bytes.Buffer{},
		Stderr:   &bytes.Buffer{},
		CWD:      &dir,
		Aliases:  alias.New(),
		History:  history.NewWithPath(filepath.Join(t.TempDir(), "hist")),
		Registry: reg,
	}
}

func TestLoad_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, `
version: "1"
project: example
tasks:
  build:
    description: "Build the project"
    commands:
      - echo one
      - echo two
    depends: [prep]
`)
	cfg, err := task.Load(filepath.Join(dir, "nex.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != "1" || cfg.Project != "example" {
		t.Fatalf("version=%q project=%q", cfg.Version, cfg.Project)
	}
	got, err := cfg.Lookup("build")
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != "Build the project" {
		t.Fatalf("description = %q", got.Description)
	}
	if len(got.Commands) != 2 || got.Commands[0] != "echo one" || got.Commands[1] != "echo two" {
		t.Fatalf("commands = %#v", got.Commands)
	}
	if len(got.Depends) != 1 || got.Depends[0] != "prep" {
		t.Fatalf("depends = %#v", got.Depends)
	}
}

func TestLoad_MalformedYAML(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "tasks: [unterminated")
	_, err := task.Load(filepath.Join(dir, "nex.yaml"))
	if err == nil || !strings.Contains(err.Error(), "invalid nex.yaml") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := task.Load(filepath.Join(t.TempDir(), "nex.yaml"))
	if err == nil || !strings.Contains(err.Error(), "nex.yaml not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestLookup_UnknownTask(t *testing.T) {
	cfg := task.Config{Tasks: map[string]task.Task{"build": {Commands: []string{"echo ok"}}}}
	_, err := cfg.Lookup("lint")
	if err == nil || !strings.Contains(err.Error(), `unknown task "lint"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestRun_UnknownTask(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, `
tasks:
  build:
    commands: ["echo ok"]
`)
	err := task.Run(shellContext(t, dir, context.Background()), "missing")
	if err == nil || !strings.Contains(err.Error(), `unknown task "missing"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestRun_EmptyTaskCommands(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, `
tasks:
  empty:
    description: "nothing"
    commands: []
`)
	err := task.Run(shellContext(t, dir, context.Background()), "empty")
	if err == nil || !strings.Contains(err.Error(), "no commands") {
		t.Fatalf("error = %v", err)
	}
}

func TestRun_MissingYAMLFile(t *testing.T) {
	err := task.Run(shellContext(t, t.TempDir(), context.Background()), "build")
	if err == nil || !strings.Contains(err.Error(), "nex.yaml not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestRun_SuccessfulAndSequential(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, `
tasks:
  build:
    commands:
      - echo first
      - echo second
  unused:
    depends: [build]
    commands:
      - echo unused
`)
	var out, errOut bytes.Buffer
	ctx := shellContext(t, dir, context.Background())
	ctx.Stdout, ctx.Stderr = &out, &errOut
	if err := task.Run(ctx, "build"); err != nil {
		t.Fatalf("run: %v stderr=%q", err, errOut.String())
	}
	if out.String() != "first\nsecond\n" {
		t.Fatalf("stdout = %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestRun_DoesNotExecuteDepends(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, `
tasks:
  build:
    commands:
      - echo from-build
  dev:
    depends: [build]
    commands:
      - echo from-dev
`)
	var out bytes.Buffer
	ctx := shellContext(t, dir, context.Background())
	ctx.Stdout = &out
	if err := task.Run(ctx, "dev"); err != nil {
		t.Fatal(err)
	}
	if out.String() != "from-dev\n" {
		t.Fatalf("stdout = %q; depends must not run", out.String())
	}
}

func TestRun_StopsAfterFirstFailedCommand(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, `
tasks:
  test:
    commands:
      - echo first
      - cat missing-nexshell-task-file
      - echo second
`)
	var out, errOut bytes.Buffer
	ctx := shellContext(t, dir, context.Background())
	ctx.Stdout, ctx.Stderr = &out, &errOut
	err := task.Run(ctx, "test")
	if err == nil || err.Error() != `task "test": command failed` {
		t.Fatalf("error = %v", err)
	}
	if out.String() != "first\n" {
		t.Fatalf("stdout = %q", out.String())
	}
	if !strings.Contains(errOut.String(), "missing-nexshell-task-file") {
		t.Fatalf("nested diagnostic missing: %q", errOut.String())
	}
	if strings.Count(errOut.String(), "command failed") != 0 {
		t.Fatalf("task-level error should not be printed by nested executor: %q", errOut.String())
	}
}

func TestRun_CommandParseFailure(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, `
tasks:
  lint:
    commands:
      - echo ok
      - "echo 'unterminated"
      - echo never
`)
	var out bytes.Buffer
	ctx := shellContext(t, dir, context.Background())
	ctx.Stdout = &out
	err := task.Run(ctx, "lint")
	if err == nil || !strings.Contains(err.Error(), "parse command 2") {
		t.Fatalf("error = %v", err)
	}
	if out.String() != "ok\n" {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestRun_Cancellation(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, `
tasks:
  build:
    commands:
      - wait-cancel
`)
	started := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	shell := shellContext(t, dir, ctx)
	shell.Registry.Register(&waitCancelCmd{started: started})

	done := make(chan error, 1)
	go func() { done <- task.Run(shell, "build") }()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("task returned before starting: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for task command to start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "canceled") {
			t.Fatalf("error = %v", err)
		}
		var exit *registry.ExitError
		if errors.As(err, &exit) {
			t.Fatalf("cancellation returned exit error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for canceled task")
	}
}

func TestRun_NestedExitDoesNotTerminateShell(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, `
tasks:
  build:
    commands:
      - exit 7
`)
	err := task.Run(shellContext(t, dir, context.Background()), "build")
	if err == nil || !strings.Contains(err.Error(), "nested exit is not allowed") {
		t.Fatalf("error = %v", err)
	}
	var exit *registry.ExitError
	if errors.As(err, &exit) {
		t.Fatalf("nested exit leaked: %v", err)
	}
}

func TestCommand_RegistrationAndPipelineSafety(t *testing.T) {
	reg := registry.New()
	builtins.RegisterAll(reg)
	cmd, ok := reg.Lookup("task")
	if !ok {
		t.Fatal("task command is not registered")
	}
	capability, ok := cmd.(registry.PipelineCommand)
	if !ok || capability.PipelineSafe() {
		t.Fatal("task must declare pipeline-unsafe")
	}
}

func TestCommand_MissingName(t *testing.T) {
	cmd := &task.Command{}
	err := cmd.Run(shellContext(t, t.TempDir(), context.Background()), nil)
	if err == nil || !strings.Contains(err.Error(), "missing task name") {
		t.Fatalf("error = %v", err)
	}
}

type waitCancelCmd struct{ started chan struct{} }

func (c *waitCancelCmd) Name() string        { return "wait-cancel" }
func (c *waitCancelCmd) Description() string { return "block until canceled" }
func (c *waitCancelCmd) PipelineSafe() bool  { return true }
func (c *waitCancelCmd) Run(ctx registry.Context, _ []string) error {
	close(c.started)
	<-ctx.Cancellation().Done()
	return ctx.Cancellation().Err()
}
