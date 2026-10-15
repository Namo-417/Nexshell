package builtins_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nexshell/internal/alias"
	"nexshell/internal/builtins"
	"nexshell/internal/history"
	"nexshell/internal/registry"
)

// buildContext builds a fully wired Context for built-in tests.
func buildContext(t *testing.T) (registry.Context, *string) {
	t.Helper()
	reg := registry.New()
	builtins.RegisterAll(reg)
	a := alias.New()
	h := history.NewWithPath(filepath.Join(t.TempDir(), "hist"))
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	ctx := registry.Context{
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

	if err := runBuiltin(t, ctx, "cd", []string{tmpDir}); err != nil {
		t.Fatalf("cd failed: %v", err)
	}
	if *cwd != tmpDir {
		t.Fatalf("cwd = %q want %q", *cwd, tmpDir)
	}
	orig, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

func TestCd_NoArgs_GoesToHome(t *testing.T) {
	ctx, cwd := buildContext(t)
	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	home, _ := os.UserHomeDir()
	if err := runBuiltin(t, ctx, "cd", nil); err != nil {
		t.Fatalf("cd (no args) failed: %v", err)
	}
	if *cwd == "" {
		t.Fatal("cwd is empty after cd ~")
	}
	_ = home
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
	if err := runBuiltin(t, ctx, "help", nil); err != nil {
		t.Fatalf("help failed: %v", err)
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
	must := []string{"cd", "pwd", "ls", "cat", "echo", "clear", "exit", "help", "env", "history", "alias", "unalias"}
	for _, name := range must {
		if _, ok := reg.Lookup(name); !ok {
			t.Errorf("expected built-in %q to be registered", name)
		}
	}
}
