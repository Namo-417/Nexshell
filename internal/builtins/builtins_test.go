package builtins_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nexshell/internal/alias"
	"nexshell/internal/builtins"
	"nexshell/internal/executor"
	"nexshell/internal/history"
	"nexshell/internal/registry"
	"nexshell/internal/task"
)

// buildContext builds a fully wired Context for built-in tests.
func buildContext(t *testing.T) (registry.Context, *string) {
	t.Helper()
	task.SetExecute(executor.Execute)
	reg := registry.New()
	builtins.RegisterAll(reg)
	a := alias.New()
	h := history.NewWithPath(filepath.Join(t.TempDir(), "hist"))
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	ctx := registry.Context{
		Stdout:   io.Discard,
		Stderr:   io.Discard,
		CWD:      &cwd,
		Aliases:  a,
		History:  h,
		Registry: reg,
	}
	return ctx, &cwd
}

func runBuiltin(t *testing.T, ctx registry.Context, name string, args []string) error {
	t.Helper()
	cmd, ok := ctx.Registry.Lookup(name)
	if !ok {
		t.Fatalf("builtin %q not found", name)
	}
	return cmd.Run(ctx, args)
}

// ─── cd ──────────────────────────────────────────────────────────────────────

func TestCd_ValidDir(t *testing.T) {
	ctx, cwd := buildContext(t)
	tmpDir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Error(err)
		}
	})

	if err := runBuiltin(t, ctx, "cd", []string{tmpDir}); err != nil {
		t.Fatalf("cd failed: %v", err)
	}
	if *cwd != tmpDir {
		t.Fatalf("cwd = %q want %q", *cwd, tmpDir)
	}
}

func TestCd_NoArgs_GoesToHome(t *testing.T) {
	ctx, cwd := buildContext(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	if err := runBuiltin(t, ctx, "cd", nil); err != nil {
		t.Fatalf("cd (no args) failed: %v", err)
	}
	if *cwd != home {
		t.Fatalf("cwd = %q want temporary home %q", *cwd, home)
	}
}

func TestCd_InvalidDir_Errors(t *testing.T) {
	ctx, _ := buildContext(t)
	err := runBuiltin(t, ctx, "cd", []string{"/this/path/does/not/exist/hopefully"})
	if err == nil {
		t.Fatal("expected error for invalid directory, got nil")
	}
}

// ─── pwd ─────────────────────────────────────────────────────────────────────

func TestPwd_PrintsCWD(t *testing.T) {
	ctx, cwd := buildContext(t)
	*cwd = "/some/fake/path"
	if err := runBuiltin(t, ctx, "pwd", nil); err != nil {
		t.Fatalf("pwd failed: %v", err)
	}
}

// ─── echo ────────────────────────────────────────────────────────────────────

func TestEcho_NoArgs(t *testing.T) {
	ctx, _ := buildContext(t)
	if err := runBuiltin(t, ctx, "echo", nil); err != nil {
		t.Fatalf("echo failed: %v", err)
	}
}

func TestEcho_MultipleArgs(t *testing.T) {
	ctx, _ := buildContext(t)
	if err := runBuiltin(t, ctx, "echo", []string{"hello", "world"}); err != nil {
		t.Fatalf("echo failed: %v", err)
	}
}

// ─── ls ──────────────────────────────────────────────────────────────────────

func TestLs_CurrentDir(t *testing.T) {
	ctx, _ := buildContext(t)
	if err := runBuiltin(t, ctx, "ls", nil); err != nil {
		t.Fatalf("ls failed: %v", err)
	}
}

func TestLs_InvalidDir_Errors(t *testing.T) {
	ctx, _ := buildContext(t)
	err := runBuiltin(t, ctx, "ls", []string{"/no/such/dir/nexshell/test"})
	if err == nil {
		t.Fatal("expected error for invalid dir, got nil")
	}
}

func TestLs_SpecificDir(t *testing.T) {
	ctx, _ := buildContext(t)
	tmpDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(tmpDir, "hello.txt"), []byte("hi"), 0644)
	if err := runBuiltin(t, ctx, "ls", []string{tmpDir}); err != nil {
		t.Fatalf("ls failed: %v", err)
	}
}

// ─── cat ─────────────────────────────────────────────────────────────────────

func TestCat_ValidFile(t *testing.T) {
	ctx, _ := buildContext(t)
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")
	_ = os.WriteFile(path, []byte("nexshell test content"), 0644)
	if err := runBuiltin(t, ctx, "cat", []string{path}); err != nil {
		t.Fatalf("cat failed: %v", err)
	}
}

func TestCat_NoArgs_Errors(t *testing.T) {
	ctx, _ := buildContext(t)
	err := runBuiltin(t, ctx, "cat", nil)
	if err == nil {
		t.Fatal("expected error for cat with no args")
	}
}

// ─── alias & unalias ─────────────────────────────────────────────────────────

func TestAlias_SetAndList(t *testing.T) {
	ctx, _ := buildContext(t)
	if err := runBuiltin(t, ctx, "alias", []string{"ll=ls -l"}); err != nil {
		t.Fatalf("alias set failed: %v", err)
	}
	val, ok := ctx.Aliases.Get("ll")
	if !ok {
		t.Fatal("expected alias 'll' to be set")
	}
	if !strings.Contains(val, "ls") {
		t.Fatalf("unexpected alias value: %q", val)
	}
}

func TestUnalias_RemovesAlias(t *testing.T) {
	ctx, _ := buildContext(t)
	ctx.Aliases.Set("foo", "bar")
	if err := runBuiltin(t, ctx, "unalias", []string{"foo"}); err != nil {
		t.Fatalf("unalias failed: %v", err)
	}
	if _, ok := ctx.Aliases.Get("foo"); ok {
		t.Fatal("expected alias 'foo' to be removed")
	}
}

func TestUnalias_NoArgs_Errors(t *testing.T) {
	ctx, _ := buildContext(t)
	err := runBuiltin(t, ctx, "unalias", nil)
	if err == nil {
		t.Fatal("expected error for unalias with no args")
	}
}

// ─── help ─────────────────────────────────────────────────────────────────────

func TestHelp_ListAll(t *testing.T) {
	ctx, _ := buildContext(t)
	var out bytes.Buffer
	ctx.Stdout = &out
	if err := runBuiltin(t, ctx, "help", nil); err != nil {
		t.Fatalf("help failed: %v", err)
	}
	if !strings.Contains(out.String(), "task") {
		t.Fatalf("help output missing task: %q", out.String())
	}
}

func TestHelp_SpecificCommand(t *testing.T) {
	ctx, _ := buildContext(t)
	if err := runBuiltin(t, ctx, "help", []string{"ls"}); err != nil {
		t.Fatalf("help ls failed: %v", err)
	}
}

func TestHelp_UnknownCommand_Errors(t *testing.T) {
	ctx, _ := buildContext(t)
	err := runBuiltin(t, ctx, "help", []string{"ghostcmd"})
	if err == nil {
		t.Fatal("expected error for help on unknown command")
	}
}

// ─── RegisterAll ─────────────────────────────────────────────────────────────

func TestRegisterAll_AllCommandsPresent(t *testing.T) {
	reg := registry.New()
	builtins.RegisterAll(reg)
	must := []string{"cd", "pwd", "ls", "cat", "echo", "clear", "exit", "help", "env", "history", "alias", "unalias", "task"}
	for _, name := range must {
		if _, ok := reg.Lookup(name); !ok {
			t.Errorf("expected built-in %q to be registered", name)
		}
	}
}

func TestBuiltins_UseOutputStream(t *testing.T) {
	t.Setenv("NEXSHELL_BUILTIN_TEST", "streamed")
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"pwd", nil, "working-directory"},
		{"echo", []string{"hello", "world"}, "hello world\n"},
		{"echo", nil, "\n"},
		{"clear", nil, "\033[H\033[2J"},
		{"help", nil, "Available Commands"},
		{"help", []string{"cat"}, "cat"},
		{"env", []string{"NEXSHELL_BUILTIN_TEST"}, "NEXSHELL_BUILTIN_TEST=streamed\n"},
		{"env", nil, "NEXSHELL_BUILTIN_TEST=streamed\n"},
		{"history", nil, "(no history)\n"},
		{"alias", nil, "(no aliases)\n"},
		{"alias", []string{"ll=ls"}, "='ls'\n"},
	} {
		t.Run(tt.name+strings.Join(tt.args, "_"), func(t *testing.T) {
			ctx, cwd := buildContext(t)
			*cwd = "working-directory"
			var out bytes.Buffer
			ctx.Stdout = &out
			if err := runBuiltin(t, ctx, tt.name, tt.args); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), tt.want) {
				t.Fatalf("output %q does not contain %q", out.String(), tt.want)
			}
		})
	}
}

func TestCat_ExplicitInput(t *testing.T) {
	ctx, _ := buildContext(t)
	ctx.Stdin = strings.NewReader("first\nsecond\n")
	var out bytes.Buffer
	ctx.Stdout = &out
	if err := runBuiltin(t, ctx, "cat", nil); err != nil {
		t.Fatal(err)
	}
	if out.String() != "first\nsecond\n" {
		t.Fatalf("output = %q", out.String())
	}
}

func TestCat_MissingFilesReturnErrorAndContinue(t *testing.T) {
	ctx, cwd := buildContext(t)
	*cwd = t.TempDir()
	if err := os.WriteFile(filepath.Join(*cwd, "present.txt"), []byte("present"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	ctx.Stdout, ctx.Stderr = &out, &diagnostics
	err := runBuiltin(t, ctx, "cat", []string{"missing.txt", "present.txt", "also-missing.txt"})
	if err == nil || !strings.Contains(err.Error(), "missing.txt") || !strings.Contains(err.Error(), "also-missing.txt") {
		t.Fatalf("expected missing-file diagnostics, got %v", err)
	}
	if out.String() != "present" {
		t.Fatalf("output = %q", out.String())
	}
	if diagnostics.Len() != 0 {
		t.Fatalf("builtin printed diagnostics that the executor must own: %q", diagnostics.String())
	}
	// The file must no longer be held open when the command returns on Windows.
	if err := os.Remove(filepath.Join(*cwd, "present.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestCat_CanceledInput(t *testing.T) {
	ctx, _ := buildContext(t)
	commandContext, cancel := context.WithCancel(context.Background())
	cancel()
	ctx.Context = commandContext
	ctx.Stdin = strings.NewReader("should not be copied")
	var out bytes.Buffer
	ctx.Stdout = &out
	if err := runBuiltin(t, ctx, "cat", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("canceled copy wrote %q", out.String())
	}
}

func TestCat_RejectsNonRegularOperand(t *testing.T) {
	ctx, _ := buildContext(t)
	var out bytes.Buffer
	ctx.Stdout = &out
	err := runBuiltin(t, ctx, "cat", []string{t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "only regular files are supported") {
		t.Fatalf("expected a nonregular-file error, got %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("nonregular operand wrote %q", out.String())
	}
}

func TestLs_UsesContextDirectoryAndOutput(t *testing.T) {
	ctx, cwd := buildContext(t)
	*cwd = t.TempDir()
	if err := os.Mkdir(filepath.Join(*cwd, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(*cwd, "nested", "only-here.txt"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	ctx.Stdout = &out
	if err := runBuiltin(t, ctx, "ls", []string{"nested"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "only-here.txt") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestAlias_PreservesExecutableQuotes(t *testing.T) {
	ctx, _ := buildContext(t)
	value := `"C:\Program Files\app.exe" --flag`
	if err := runBuiltin(t, ctx, "alias", []string{"app=" + value}); err != nil {
		t.Fatal(err)
	}
	if got, _ := ctx.Aliases.Get("app"); got != value {
		t.Fatalf("alias value = %q want %q", got, value)
	}
}

func TestHistory_PrintsEntries(t *testing.T) {
	ctx, _ := buildContext(t)
	ctx.History.Add("echo first")
	ctx.History.Add("echo second")
	var out bytes.Buffer
	ctx.Stdout = &out
	if err := runBuiltin(t, ctx, "history", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "1  echo first\n") || !strings.Contains(out.String(), "2  echo second\n") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestEnv_EmptyValueAndMissingVariable(t *testing.T) {
	ctx, _ := buildContext(t)
	t.Setenv("NEXSHELL_EMPTY_TEST", "")
	var out bytes.Buffer
	ctx.Stdout = &out
	if err := runBuiltin(t, ctx, "env", []string{"NEXSHELL_EMPTY_TEST"}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "NEXSHELL_EMPTY_TEST=\n" {
		t.Fatalf("output = %q", out.String())
	}
	key := "NEXSHELL_UNSET_BUILTIN_TEST_76DC"
	previous, existed := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	if existed {
		t.Cleanup(func() { _ = os.Setenv(key, previous) })
	}
	if err := runBuiltin(t, ctx, "env", []string{key}); err == nil {
		t.Fatal("expected missing environment variable error")
	}
}

func TestExit_ReturnsControlSignal(t *testing.T) {
	for _, tt := range []struct {
		args []string
		code int
	}{
		{nil, 0},
		{[]string{"7"}, 7},
		{[]string{"255"}, 255},
	} {
		ctx, _ := buildContext(t)
		err := runBuiltin(t, ctx, "exit", tt.args)
		var exit *registry.ExitError
		if !errors.As(err, &exit) || exit.Code != tt.code {
			t.Fatalf("exit %v returned %v, want code %d", tt.args, err, tt.code)
		}
	}
	for _, args := range [][]string{{"bad"}, {"-1"}, {"256"}, {"0", "1"}} {
		ctx, _ := buildContext(t)
		err := runBuiltin(t, ctx, "exit", args)
		var exit *registry.ExitError
		if err == nil || errors.As(err, &exit) {
			t.Fatalf("invalid exit %v returned %v", args, err)
		}
	}
}

func TestBuiltins_PipelineSafety(t *testing.T) {
	ctx, _ := buildContext(t)
	unsafe := map[string]bool{"cd": true, "exit": true, "alias": true, "unalias": true, "task": true}
	for _, cmd := range ctx.Registry.All() {
		capability, ok := cmd.(registry.PipelineCommand)
		if !ok || capability.PipelineSafe() == unsafe[cmd.Name()] {
			t.Errorf("command %q pipeline safety incorrect", cmd.Name())
		}
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestBuiltins_PropagateOutputFailures(t *testing.T) {
	want := errors.New("closed output")
	for _, name := range []string{"pwd", "echo", "clear", "help", "env", "history", "alias", "ls", "cat"} {
		t.Run(name, func(t *testing.T) {
			ctx, _ := buildContext(t)
			ctx.Stdout = failingWriter{want}
			ctx.Stdin = strings.NewReader("input")
			if err := runBuiltin(t, ctx, name, nil); !errors.Is(err, want) {
				t.Fatalf("expected output error, got %v", err)
			}
		})
	}
}
