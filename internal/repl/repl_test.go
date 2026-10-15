package repl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"nexshell/internal/alias"
	"nexshell/internal/builtins"
	"nexshell/internal/history"
	"nexshell/internal/parser"
	"nexshell/internal/registry"
)

func testShell(t *testing.T) *Shell {
	t.Helper()
	reg := registry.New()
	builtins.RegisterAll(reg)
	s, err := New(reg, alias.New(), history.NewWithPath(filepath.Join(t.TempDir(), "history")))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func runScript(t *testing.T, s *Shell, script string) (int, string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out, errOut bytes.Buffer
	code := s.RunContext(ctx, strings.NewReader(script), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestScriptRegression(t *testing.T) {
	s := testShell(t)
	historyPath := filepath.Join(t.TempDir(), "history")
	s.hist = history.NewWithPath(historyPath)
	code, out, errOut := runScript(t, s, "# comment\nalias say='echo'\nsay \"a  b\"\nunalias say\necho \"\" tail\necho\ttabbed\necho end\\ \nhelp\nhistory\nclear\nexit 7\necho NEVER\n")
	if code != 7 || errOut != "" {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	for _, want := range []string{"a  b\n", " tail\n", "tabbed\n", "end \n", "Available Commands", "alias say=", "\033[H\033[2J", "nexshell"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output %q", want, out)
		}
	}
	if strings.Contains(out, "NEVER") {
		t.Fatal("executed input after exit")
	}
	if _, ok := s.aliases.Get("say"); ok {
		t.Fatal("alias was not removed")
	}
	loaded := history.NewWithPath(historyPath)
	if err := loaded.Load(); err != nil {
		t.Fatal(err)
	}
	if loaded.Len() != s.hist.Len() || loaded.Len() == 0 {
		t.Fatalf("persisted=%q session=%q", loaded.List(), s.hist.List())
	}
}

func TestSyntaxFailureDoesNotExecuteOrExit(t *testing.T) {
	for _, bad := range []string{"echo bad |", `echo "unclosed`, "echo nope >", "| echo nope", "echo nope && echo bad"} {
		t.Run(bad, func(t *testing.T) {
			code, out, errOut := runScript(t, testShell(t), bad+"\necho RECOVERED\n")
			if code != 0 || !strings.Contains(out, "RECOVERED\n") || errOut == "" {
				t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
			}
			if strings.Contains(out, "nope\n") {
				t.Fatal("partially executed invalid command")
			}
		})
	}
}

func TestFailureExitStatuses(t *testing.T) {
	tests := []struct {
		line string
		code int
	}{{"missing-nexshell-command-012345", 127}, {"echo bad |", 2}, {"exit 23", 23}}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			code, _, _ := runScript(t, testShell(t), tt.line+"\n")
			if code != tt.code {
				t.Fatalf("code=%d want=%d", code, tt.code)
			}
		})
	}
}

func TestRedirectionAndPipeline(t *testing.T) {
	s := testShell(t)
	dir := t.TempDir()
	first := filepath.Join(dir, "file with spaces.txt")
	second := filepath.Join(dir, "copy.txt")
	errFile := filepath.Join(dir, "errors.txt")
	script := fmt.Sprintf("echo hello > %q\necho another >> %q\ncat < %q | cat > %q\ncat missing-nexshell-file 2> %q\necho RECOVERED\n", first, first, first, second, errFile)
	code, out, errOut := runScript(t, s, script)
	if code != 0 || errOut != "" || !strings.Contains(out, "RECOVERED") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
	b, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "hello\nanother\n" {
		t.Fatalf("redirected=%q", b)
	}
	b, err = os.ReadFile(errFile)
	if err != nil || !bytes.Contains(b, []byte("missing-nexshell-file")) {
		t.Fatalf("stderr redirect=%q err=%v", b, err)
	}
}

func TestLongLineAndUnbufferedChildInput(t *testing.T) {
	// The child reads the stream after its command line; the REPL must not
	// consume its input through read-ahead.
	t.Setenv("NEXSHELL_REPL_FIXTURE", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runScript(t, testShell(t), fmt.Sprintf("%q -test.run=^TestREPLChildProcess$\nCHILD_INPUT\n", exe))
	if code != 0 || errOut != "" || strings.Count(out, "CHILD_INPUT") != 1 {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
	large := strings.Repeat("x", 70000)
	code, out, errOut = runScript(t, testShell(t), "echo "+large+"\necho AFTER_LONG_LINE\n")
	if code != 0 || errOut != "" || !strings.Contains(out, large) || !strings.Contains(out, "AFTER_LONG_LINE") {
		t.Fatalf("long input failed: code=%d stderr=%q", code, errOut)
	}
}

func TestREPLChildProcess(t *testing.T) {
	if os.Getenv("NEXSHELL_REPL_FIXTURE") != "1" {
		return
	}
	if _, err := io.Copy(os.Stdout, os.Stdin); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestAliasPlans(t *testing.T) {
	s := testShell(t)
	s.aliases.Set("say", "echo")
	s.aliases.Set("pipe", "echo fixed | cat")
	s.aliases.Set("app", `"C:\Program Files\app.exe"`)
	for _, tt := range []struct {
		line  string
		names []string
		args  []string
	}{
		{`say "a  b"`, []string{"echo"}, []string{"a  b"}},
		{`pipe`, []string{"echo", "cat"}, nil},
		{`app ""`, []string{`C:\Program Files\app.exe`}, []string{""}},
	} {
		plan, err := parser.Parse(tt.line)
		if err != nil {
			t.Fatal(err)
		}
		plan, err = s.expandAliases(plan)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Commands) != len(tt.names) {
			t.Fatalf("plan=%+v", plan)
		}
		for i, name := range tt.names {
			if plan.Commands[i].Name != name {
				t.Fatalf("plan=%+v", plan)
			}
		}
		if tt.args != nil && !slices.Equal(plan.Commands[len(plan.Commands)-1].Args, tt.args) {
			t.Fatalf("args=%q", plan.Commands[len(plan.Commands)-1].Args)
		}
	}
	s.aliases.Set("bad", "echo > one")
	plan, _ := parser.Parse("bad > two")
	if _, err := s.expandAliases(plan); err == nil {
		t.Fatal("accepted duplicate alias redirection")
	}
	s.aliases.Set("bad", "echo |")
	plan, _ = parser.Parse("bad")
	if _, err := s.expandAliases(plan); err == nil {
		t.Fatal("accepted malformed alias")
	}
	s.aliases.Set("filter", "cat | cat")
	plan, err := parser.Parse("filter < input.txt > output.txt")
	if err != nil {
		t.Fatal(err)
	}
	plan, err = s.expandAliases(plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Commands[0].Redirections) != 1 || plan.Commands[0].Redirections[0].Kind != parser.Input ||
		len(plan.Commands[1].Redirections) != 1 || plan.Commands[1].Redirections[0].Kind != parser.Output {
		t.Fatalf("alias pipeline redirects=%+v", plan)
	}
}

type blockingCmd struct{ started chan struct{} }

func (*blockingCmd) Name() string        { return "block" }
func (*blockingCmd) Description() string { return "test cancellation" }
func (b *blockingCmd) Run(ctx registry.Context, _ []string) error {
	close(b.started)
	<-ctx.Cancellation().Done()
	return ctx.Cancellation().Err()
}

func TestInterruptCancelsForegroundAndContinues(t *testing.T) {
	s := testShell(t)
	command := &blockingCmd{started: make(chan struct{})}
	s.reg.Register(command)
	signals := make(chan os.Signal, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() {
		select {
		case <-command.started:
			signals <- os.Interrupt
		case <-ctx.Done():
		}
	}()
	var out, errOut bytes.Buffer
	code := s.run(ctx, strings.NewReader("block\necho AFTER_INTERRUPT\nexit\n"), &out, &errOut, signals)
	if code != 0 || !strings.Contains(out.String(), "AFTER_INTERRUPT") {
		t.Fatalf("code=%d out=%q err=%q", code, out.String(), errOut.String())
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("broken input") }
func TestInputErrors(t *testing.T) {
	var out, errOut bytes.Buffer
	code := testShell(t).RunContext(context.Background(), failingReader{}, &out, &errOut)
	if code != 1 || !strings.Contains(errOut.String(), "broken input") {
		t.Fatalf("code=%d err=%q", code, errOut.String())
	}
}

func TestReadLineDoesNotReadAhead(t *testing.T) {
	in := strings.NewReader("first\nremaining")
	line, err := readLine(in)
	if line != "first" || err != nil {
		t.Fatalf("%q %v", line, err)
	}
	rest, _ := io.ReadAll(in)
	if string(rest) != "remaining" {
		t.Fatalf("remaining=%q", rest)
	}
}

func TestAbbreviateHomeBoundary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if got := abbreviateHome(home); got != "~" {
		t.Fatalf("got=%q", got)
	}
	neighbor := home + "-neighbor"
	if got := abbreviateHome(neighbor); got != neighbor {
		t.Fatalf("neighbor=%q", got)
	}
}

type promptObserver struct {
	bytes.Buffer
	prompts chan struct{}
}

func (w *promptObserver) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	if bytes.Contains(p, []byte(" ❯ ")) {
		select {
		case w.prompts <- struct{}{}:
		default:
		}
	}
	return n, err
}
func TestIdleInterruptKeepsShellAlive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	input, feed := io.Pipe()
	defer input.Close()
	defer feed.Close()
	signals := make(chan os.Signal, 1)
	out := &promptObserver{prompts: make(chan struct{}, 4)}
	var errOut bytes.Buffer
	writerDone := make(chan error, 1)
	go func() {
		select {
		case <-out.prompts:
		case <-ctx.Done():
			writerDone <- ctx.Err()
			return
		}
		signals <- os.Interrupt
		select {
		case <-out.prompts:
		case <-ctx.Done():
			writerDone <- ctx.Err()
			return
		}
		_, err := io.WriteString(feed, "echo AFTER_IDLE_INTERRUPT\nexit\n")
		writerDone <- err
	}()
	code := testShell(t).run(ctx, input, out, &errOut, signals)
	if err := <-writerDone; err != nil {
		t.Fatal(err)
	}
	if code != 0 || !strings.Contains(out.String(), "AFTER_IDLE_INTERRUPT") || errOut.Len() != 0 {
		t.Fatalf("code=%d out=%q err=%q", code, out.String(), errOut.String())
	}
}
func TestCdPersistsAndPipelineCdRejected(t *testing.T) {
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	t.Cleanup(func() {
		if err := os.Chdir(original); err != nil {
			t.Error(err)
		}
	})
	s := testShell(t)
	code, out, diagnostic := runScript(t, s, fmt.Sprintf("cd %q\npwd\ncd .. | cat\npwd\n", directory))
	if code != 0 || strings.Count(out, directory) < 2 || !strings.Contains(diagnostic, "cannot run in a pipeline") {
		t.Fatalf("code=%d out=%q err=%q", code, out, diagnostic)
	}
	if s.cwd != directory {
		t.Fatalf("cwd changed by pipeline: %q", s.cwd)
	}
}
func TestHistorySaveFailureReported(t *testing.T) {
	s := testShell(t)
	s.hist = history.NewWithPath(filepath.Join(t.TempDir(), "missing-directory", "history"))
	code, _, diagnostic := runScript(t, s, "echo done\n")
	if code != 1 || !strings.Contains(diagnostic, "could not save history") {
		t.Fatalf("code=%d err=%q", code, diagnostic)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken output") }
func TestPromptWriteFailure(t *testing.T) {
	var diagnostic bytes.Buffer
	code := testShell(t).RunContext(context.Background(), strings.NewReader("echo NEVER\n"), failingWriter{}, &diagnostic)
	if code != 1 || !strings.Contains(diagnostic.String(), "broken output") {
		t.Fatalf("code=%d err=%q", code, diagnostic.String())
	}
}

type interruptedReader struct {
	err  error
	rest io.Reader
}

func (r *interruptedReader) Read(p []byte) (int, error) {
	if r.err != nil {
		err := r.err
		r.err = nil
		return 0, err
	}
	return r.rest.Read(p)
}
func assertReadInterruptRecovery(t *testing.T, interrupt error) {
	t.Helper()
	var out, diagnostics bytes.Buffer
	code := testShell(t).RunContext(context.Background(), &interruptedReader{err: interrupt, rest: strings.NewReader("echo RECOVERED_READ\nexit\n")}, &out, &diagnostics)
	if code != 0 || diagnostics.Len() != 0 || !strings.Contains(out.String(), "RECOVERED_READ") {
		t.Fatalf("code=%d out=%q err=%q", code, out.String(), diagnostics.String())
	}
}
