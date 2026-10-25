package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"nexshell/internal/builtins"
	"nexshell/internal/parser"
	"nexshell/internal/registry"
)

// The test executable is its own portable command fixture; no system sort,
// grep, cat, shell, or timing-dependent sleep utility is required.
func TestExecutorHelperProcess(t *testing.T) {
	index := -1
	for i, arg := range os.Args {
		if arg == "--nexshell-fixture" {
			index = i + 1
			break
		}
	}
	if index < 0 {
		return
	}
	args := os.Args[index:]
	switch args[0] {
	case "emit":
		fmt.Fprint(os.Stdout, args[1])
	case "copy":
		_, _ = io.Copy(os.Stdout, os.Stdin)
	case "upper":
		input, _ := io.ReadAll(os.Stdin)
		fmt.Fprint(os.Stdout, strings.ToUpper(string(input)))
	case "sort":
		input, _ := io.ReadAll(os.Stdin)
		lines := strings.Split(strings.TrimSuffix(string(input), "\n"), "\n")
		sort.Strings(lines)
		fmt.Fprintln(os.Stdout, strings.Join(lines, "\n"))
	case "fail":
		fmt.Fprintln(os.Stderr, "fixture error")
		code, _ := strconv.Atoi(args[1])
		os.Exit(code)
	case "silent-fail":
		code, _ := strconv.Atoi(args[1])
		os.Exit(code)
	case "large":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), 2*1024*1024))
	case "wait":
		if err := os.WriteFile(args[1], []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(90)
		}
		fmt.Fprintln(os.Stdout, "ready")
		time.Sleep(time.Hour)
	case "wait-stderr":
		fmt.Fprintln(os.Stderr, "ready")
		time.Sleep(time.Hour)
	case "interrupt":
		process, _ := os.FindProcess(os.Getpid())
		_ = process.Signal(os.Interrupt)
		time.Sleep(time.Second)
		os.Exit(93) // Unix's default SIGINT handling should have terminated us.
	case "tree":
		if err := os.WriteFile(args[1]+".parent", []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(90)
		}
		child := exec.Command(os.Args[0], helperArgs("wait", args[1]+".child")...)
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(91)
		}
		_ = child.Wait()
	case "environment":
		fmt.Fprintf(os.Stdout, "%s\n", os.Getenv("NEXSHELL_FIXTURE_VALUE"))
		cwd, _ := os.Getwd()
		fmt.Fprint(os.Stdout, cwd)
	default:
		os.Exit(92)
	}
	os.Exit(0)
}

func helperArgs(mode string, args ...string) []string {
	result := []string{"-test.run=^TestExecutorHelperProcess$", "--", "--nexshell-fixture", mode}
	return append(result, args...)
}

func fixture(t *testing.T, mode string, args ...string) parser.Command {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return parser.Command{Name: executable, Args: helperArgs(mode, args...)}
}

func runPlan(t *testing.T, plan parser.Plan, dir string, input io.Reader) (int, error, string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var out, diagnostic bytes.Buffer
	reg := registry.New()
	builtins.RegisterAll(reg)
	status, err := Execute(ctx, plan, registry.Context{
		CWD: &dir, Registry: reg, Stdin: input, Stdout: &out, Stderr: &diagnostic,
	})
	if ctx.Err() != nil {
		t.Fatalf("execution timed out: %v, stderr %q", ctx.Err(), diagnostic.String())
	}
	return status, err, out.String(), diagnostic.String()
}

func TestExternalCommandsAndPipelines(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name   string
		stages []parser.Command
		want   string
	}{
		{"external", []parser.Command{fixture(t, "emit", "hello\n")}, "hello\n"},
		{"two stages", []parser.Command{fixture(t, "emit", "hello\n"), fixture(t, "upper")}, "HELLO\n"},
		{"three stages", []parser.Command{fixture(t, "emit", "b\na\n"), fixture(t, "upper"), fixture(t, "sort")}, "A\nB\n"},
		{"builtin producer", []parser.Command{{Name: "echo", Args: []string{"hello"}}, fixture(t, "upper")}, "HELLO\n"},
		{"builtin consumer", []parser.Command{fixture(t, "emit", "hello\n"), {Name: "cat"}}, "hello\n"},
		{"builtin stages", []parser.Command{{Name: "echo", Args: []string{"hello"}}, {Name: "cat"}}, "hello\n"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			status, err, got, diagnostics := runPlan(t, parser.Plan{Commands: test.stages}, dir, strings.NewReader(""))
			if err != nil || status != 0 || got != test.want || diagnostics != "" {
				t.Fatalf("status=%d err=%v stdout=%q stderr=%q; want %q", status, err, got, diagnostics, test.want)
			}
		})
	}
}

func TestPipelineDrainsLargeOutput(t *testing.T) {
	status, err, out, diagnostics := runPlan(t, parser.Plan{Commands: []parser.Command{
		fixture(t, "large"), fixture(t, "copy"), {Name: "cat"},
	}}, t.TempDir(), strings.NewReader(""))
	if err != nil || status != 0 || len(out) != 2*1024*1024 || diagnostics != "" {
		t.Fatalf("status=%d err=%v output bytes=%d stderr=%q", status, err, len(out), diagnostics)
	}
}

func TestStatusAndLookupErrors(t *testing.T) {
	t.Run("unknown", func(t *testing.T) {
		status, err, out, diagnostics := runPlan(t, parser.Plan{Commands: []parser.Command{{Name: "nexshell-command-that-does-not-exist-5819"}}}, t.TempDir(), nil)
		var missing *NotFoundError
		if status != 127 || !errors.As(err, &missing) || out != "" || !strings.Contains(diagnostics, "command not found") {
			t.Fatalf("status=%d err=%v stdout=%q stderr=%q", status, err, out, diagnostics)
		}
	})
	for _, test := range []struct {
		name     string
		commands []parser.Command
		want     int
	}{
		{"external nonzero", []parser.Command{fixture(t, "fail", "7")}, 7},
		{"external silent nonzero", []parser.Command{fixture(t, "silent-fail", "8")}, 8},
		{"early stage failure", []parser.Command{fixture(t, "fail", "7"), fixture(t, "copy")}, 7},
		{"rightmost failure", []parser.Command{fixture(t, "fail", "7"), fixture(t, "fail", "9")}, 9},
		{"builtin failure", []parser.Command{{Name: "cat", Args: []string{"absent-file.txt"}}}, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, err, _, diagnostics := runPlan(t, parser.Plan{Commands: test.commands}, t.TempDir(), nil)
			var exited *ExecError
			if status != test.want || !errors.As(err, &exited) || exited.ExitCode != test.want || diagnostics == "" {
				t.Fatalf("status=%d err=%v stderr=%q", status, err, diagnostics)
			}
		})
	}
}

func TestRedirections(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "output with spaces.txt")
	if err := os.WriteFile(output, []byte("old content should disappear"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		command parser.Command
	}{
		{"overwrite", parser.Command{Name: "echo", Args: []string{"first"}, Redirections: []parser.Redirection{{Kind: parser.Output, Target: output}}}},
		{"append", parser.Command{Name: "echo", Args: []string{"second"}, Redirections: []parser.Redirection{{Kind: parser.Append, Target: output}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, err, out, diagnostics := runPlan(t, parser.Plan{Commands: []parser.Command{test.command}}, dir, nil)
			if err != nil || status != 0 || out != "" || diagnostics != "" {
				t.Fatalf("status=%d err=%v out=%q stderr=%q", status, err, out, diagnostics)
			}
		})
	}
	content, err := os.ReadFile(output)
	if err != nil || string(content) != "first\nsecond\n" {
		t.Fatalf("content=%q err=%v", content, err)
	}
	for _, command := range []parser.Command{fixture(t, "upper"), {Name: "cat"}} {
		command.Redirections = []parser.Redirection{{Kind: parser.Input, Target: output}}
		status, err, out, diagnostics := runPlan(t, parser.Plan{Commands: []parser.Command{command}}, dir, nil)
		want := "first\nsecond\n"
		if command.Name != "cat" {
			want = strings.ToUpper(want)
		}
		if status != 0 || err != nil || out != want || diagnostics != "" {
			t.Fatalf("input redirect: status=%d err=%v out=%q stderr=%q", status, err, out, diagnostics)
		}
	}
	// Append creates a missing file as well as preserving an existing file.
	created := parser.Command{Name: "echo", Args: []string{"created"}, Redirections: []parser.Redirection{{Kind: parser.Append, Target: "new.txt"}}}
	status, err, _, _ := runPlan(t, parser.Plan{Commands: []parser.Command{created}}, dir, nil)
	if err != nil || status != 0 {
		t.Fatalf("append create status=%d err=%v", status, err)
	}
	if content, err := os.ReadFile(filepath.Join(dir, "new.txt")); err != nil || string(content) != "created\n" {
		t.Fatalf("append create content=%q err=%v", content, err)
	}
}

func TestPipelineRedirections(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "input.txt"), []byte("b\na\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := fixture(t, "upper")
	first.Redirections = []parser.Redirection{{Kind: parser.Input, Target: "input.txt"}}
	last := fixture(t, "sort")
	last.Redirections = []parser.Redirection{{Kind: parser.Output, Target: "result.txt"}}
	status, err, out, diagnostics := runPlan(t, parser.Plan{Commands: []parser.Command{first, {Name: "cat"}, last}}, dir, nil)
	if status != 0 || err != nil || out != "" || diagnostics != "" {
		t.Fatalf("status=%d err=%v out=%q stderr=%q", status, err, out, diagnostics)
	}
	content, err := os.ReadFile(filepath.Join(dir, "result.txt"))
	if err != nil || string(content) != "A\nB\n" {
		t.Fatalf("content=%q err=%v", content, err)
	}
}

func TestStderrRedirectionAndOpenFailures(t *testing.T) {
	for _, test := range []struct {
		name    string
		command parser.Command
		status  int
		text    string
	}{
		{"external", fixture(t, "fail", "4"), 4, "fixture error"},
		{"external status", fixture(t, "silent-fail", "4"), 4, "exit status 4"},
		{"builtin", parser.Command{Name: "cat", Args: []string{"missing.txt"}}, 1, "cat:"},
		{"lookup", parser.Command{Name: "nexshell-absent-executable-9428"}, 127, "command not found"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			command := test.command
			command.Redirections = []parser.Redirection{{Kind: parser.Stderr, Target: "errors.txt"}}
			status, err, out, diagnostics := runPlan(t, parser.Plan{Commands: []parser.Command{command}}, dir, nil)
			if status != test.status || err == nil || out != "" || diagnostics != "" {
				t.Fatalf("status=%d err=%v out=%q stderr=%q", status, err, out, diagnostics)
			}
			content, err := os.ReadFile(filepath.Join(dir, "errors.txt"))
			if err != nil || !strings.Contains(string(content), test.text) {
				t.Fatalf("content=%q err=%v", content, err)
			}
		})
	}
	for _, redirect := range []parser.Redirection{
		{Kind: parser.Input, Target: "missing.txt"},
		{Kind: parser.Output, Target: "missing-dir/output.txt"},
		{Kind: parser.Append, Target: "missing-dir/output.txt"},
		{Kind: parser.Stderr, Target: "missing-dir/errors.txt"},
	} {
		status, err, out, diagnostics := runPlan(t, parser.Plan{Commands: []parser.Command{{Name: "echo", Args: []string{"hello"}, Redirections: []parser.Redirection{redirect}}}}, t.TempDir(), nil)
		var redirectErr *RedirectionError
		if status != 1 || !errors.As(err, &redirectErr) || out != "" || !strings.Contains(diagnostics, "redirect") {
			t.Fatalf("status=%d err=%v out=%q stderr=%q", status, err, out, diagnostics)
		}
	}
}

func TestPreflightAndPartialStartupFailure(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "preserved.txt")
	if err := os.WriteFile(output, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err, _, _ := runPlan(t, parser.Plan{Commands: []parser.Command{{Name: "nexshell-absent-executable-9428", Redirections: []parser.Redirection{{Kind: parser.Output, Target: output}}}}}, dir, nil)
	if status != 127 || err == nil {
		t.Fatalf("status=%d err=%v", status, err)
	}
	content, _ := os.ReadFile(output)
	if string(content) != "keep me" {
		t.Fatalf("lookup failure truncated output: %q", content)
	}
	// An existing, executable-permission file passes lookup but cannot start.
	bad := filepath.Join(dir, "bad.exe")
	if err := os.WriteFile(bad, []byte("invalid executable format"), 0o700); err != nil {
		t.Fatal(err)
	}
	second := parser.Command{Name: bad, Redirections: []parser.Redirection{{Kind: parser.Stderr, Target: "startup-errors.txt"}}}
	status, err, _, diagnostics := runPlan(t, parser.Plan{Commands: []parser.Command{fixture(t, "wait", filepath.Join(dir, "pid")), second}}, dir, nil)
	var startErr *StartError
	if status != 126 || !errors.As(err, &startErr) || diagnostics != "" {
		t.Fatalf("status=%d err=%v stderr=%q", status, err, diagnostics)
	}
	content, readErr := os.ReadFile(filepath.Join(dir, "startup-errors.txt"))
	if readErr != nil || !strings.Contains(string(content), "start ") {
		t.Fatalf("startup diagnostic=%q err=%v", content, readErr)
	}
	// If the first process reached the fixture, it must no longer be running.
	if content, readErr := os.ReadFile(filepath.Join(dir, "pid")); readErr == nil {
		pid, _ := strconv.Atoi(string(content))
		assertProcessStopped(t, pid)
	}
}

type readyWriter struct {
	cancel context.CancelFunc
	seen   bytes.Buffer
}

func (w *readyWriter) Write(data []byte) (int, error) {
	w.seen.Write(data)
	if bytes.Contains(w.seen.Bytes(), []byte("ready")) {
		w.cancel()
	}
	return len(data), nil
}

func TestCancellationCleansProcessTree(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pidbase := filepath.Join(dir, "tree")
	var diagnostics bytes.Buffer
	status, err := Execute(ctx, parser.Plan{Commands: []parser.Command{fixture(t, "tree", pidbase)}}, registry.Context{
		CWD: &dir, Stdin: strings.NewReader(""), Stdout: &readyWriter{cancel: cancel}, Stderr: &diagnostics,
	})
	if status != 130 || !errors.Is(err, context.Canceled) {
		t.Fatalf("status=%d err=%v stderr=%q", status, err, diagnostics.String())
	}
	for _, suffix := range []string{".parent", ".child"} {
		content, err := os.ReadFile(pidbase + suffix)
		if err != nil {
			t.Fatalf("fixture did not report %s readiness: %v", suffix, err)
		}
		pid, err := strconv.Atoi(string(content))
		if err != nil {
			t.Fatal(err)
		}
		assertProcessStopped(t, pid)
	}
}

type awaitCancellationCommand struct{ completed chan struct{} }

func (c awaitCancellationCommand) Name() string        { return "await-cancellation" }
func (c awaitCancellationCommand) Description() string { return "wait for command cancellation" }
func (c awaitCancellationCommand) PipelineSafe() bool  { return true }
func (c awaitCancellationCommand) Run(ctx registry.Context, _ []string) error {
	<-ctx.Cancellation().Done()
	close(c.completed)
	return ctx.Cancellation().Err()
}

func TestCancellationStopsCooperativeBuiltin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	completed := make(chan struct{})
	reg := registry.New()
	reg.Register(awaitCancellationCommand{completed})
	status, err := Execute(ctx, parser.Plan{Commands: []parser.Command{
		{Name: "await-cancellation"}, fixture(t, "wait-stderr"),
	}}, registry.Context{Registry: reg, Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: &readyWriter{cancel: cancel}})
	if status != 130 || !errors.Is(err, context.Canceled) {
		t.Fatalf("status=%d err=%v", status, err)
	}
	select {
	case <-completed:
	default:
		t.Fatal("builtin was not waited on")
	}
}

func TestEnvironmentWorkingDirectoryAndPATH(t *testing.T) {
	t.Setenv("NEXSHELL_FIXTURE_VALUE", "inherited")
	dir := t.TempDir()
	status, err, out, diagnostics := runPlan(t, parser.Plan{Commands: []parser.Command{fixture(t, "environment")}}, dir, nil)
	if status != 0 || err != nil || out != "inherited\n"+dir || diagnostics != "" {
		t.Fatalf("status=%d err=%v out=%q stderr=%q", status, err, out, diagnostics)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(executable))
	command := fixture(t, "emit", "found through PATH")
	command.Name = filepath.Base(executable)
	status, err, out, diagnostics = runPlan(t, parser.Plan{Commands: []parser.Command{command}}, dir, nil)
	if status != 0 || err != nil || out != "found through PATH" || diagnostics != "" {
		t.Fatalf("PATH status=%d err=%v out=%q stderr=%q", status, err, out, diagnostics)
	}
}

func TestStatefulPipelineRejectionAndExit(t *testing.T) {
	for _, name := range []string{"cd", "exit", "alias", "unalias", "task"} {
		status, err, _, diagnostics := runPlan(t, parser.Plan{Commands: []parser.Command{{Name: name}, {Name: "cat"}}}, t.TempDir(), nil)
		if status != 2 || err == nil || !strings.Contains(diagnostics, "cannot run in a pipeline") {
			t.Fatalf("%s: status=%d err=%v stderr=%q", name, status, err, diagnostics)
		}
	}
	status, err, out, diagnostics := runPlan(t, parser.Plan{Commands: []parser.Command{{Name: "exit", Args: []string{"7"}}}}, t.TempDir(), nil)
	var exit *registry.ExitError
	if status != 7 || !errors.As(err, &exit) || out != "" || diagnostics != "" {
		t.Fatalf("exit status=%d err=%v out=%q stderr=%q", status, err, out, diagnostics)
	}
}

func TestInvalidProgrammaticPlanHasNoSideEffects(t *testing.T) {
	dir := t.TempDir()
	plan := parser.Plan{Commands: []parser.Command{{Name: "echo", Redirections: []parser.Redirection{{Kind: parser.Output, Target: "should-not-exist.txt"}}}, {Name: "cat"}}}
	status, err, _, _ := runPlan(t, plan, dir, nil)
	if status != 2 || err == nil {
		t.Fatalf("status=%d err=%v", status, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "should-not-exist.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid plan created a file: %v", err)
	}
}
