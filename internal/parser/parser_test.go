package parser_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"nexshell/internal/parser"
)

func TestParseValid(t *testing.T) {
	tests := []struct {
		name string
		line string
		want parser.Plan
	}{
		{name: "empty"},
		{name: "whitespace", line: " \t  "},
		{name: "command", line: "pwd", want: plan(command("pwd"))},
		{name: "arguments", line: "echo hello world", want: plan(command("echo", "hello", "world"))},
		{name: "tabs", line: "\techo\t hello\tworld ", want: plan(command("echo", "hello", "world"))},
		{name: "double quotes", line: `echo "hello world"`, want: plan(command("echo", "hello world"))},
		{name: "single quotes", line: `cat 'file with spaces.txt'`, want: plan(command("cat", "file with spaces.txt"))},
		{name: "empty arguments", line: `echo '' "" end`, want: plan(command("echo", "", "", "end"))},
		{name: "adjacent groups", line: `echo pre"two words"'tail'`, want: plan(command("echo", "pretwo wordstail"))},
		{name: "operators in quotes", line: `echo '| < > >> 2> && ;'`, want: plan(command("echo", "| < > >> 2> && ;"))},
		{name: "single quotes literal", line: `echo 'C:\Users\name\' 'a"b'`, want: plan(command("echo", `C:\Users\name\`, `a"b`))},
		{name: "windows path", line: `cat C:\Users\name\app.log`, want: plan(command("cat", `C:\Users\name\app.log`))},
		{name: "windows quoted path", line: `cat "C:\Program Files\app.log"`, want: plan(command("cat", `C:\Program Files\app.log`))},
		{name: "windows trailing slash", line: `cd C:\Users\`, want: plan(command("cd", `C:\Users\`))},
		{name: "UNC single quoted", line: `cat '\\server\share\app.log'`, want: plan(command("cat", `\\server\share\app.log`))},
		{name: "escaped space", line: `cat file\ with\ spaces.txt`, want: plan(command("cat", "file with spaces.txt"))},
		{name: "escaped operators", line: `echo \| \< \> \& \;`, want: plan(command("echo", "|", "<", ">", "&", ";"))},
		{name: "escaped quotes", line: `echo \"hello\" \'world\'`, want: plan(command("echo", `"hello"`, `'world'`))},
		{name: "double quote escapes", line: `echo "a\"b\\c\d"`, want: plan(command("echo", `a"b\c\d`))},
		{name: "literal interpolation", line: `echo '$HOME' "$HOME" *.go`, want: plan(command("echo", "$HOME", "$HOME", "*.go"))},
		{name: "unicode", line: `echo "नमस्ते 世界"`, want: plan(command("echo", "नमस्ते 世界"))},
		{name: "two stages", line: `ls|sort`, want: plan(command("ls"), command("sort"))},
		{name: "three stages", line: `cat app.log | grep "connection failed" | sort`, want: plan(command("cat", "app.log"), command("grep", "connection failed"), command("sort"))},
		{name: "output", line: `echo hello>out.txt`, want: plan(redirect(command("echo", "hello"), parser.Output, "out.txt"))},
		{name: "append", line: `echo hello >> "some output.txt"`, want: plan(redirect(command("echo", "hello"), parser.Append, "some output.txt"))},
		{name: "input", line: `sort<input.txt`, want: plan(redirect(command("sort"), parser.Input, "input.txt"))},
		{name: "stderr", line: `app 2>errors.log`, want: plan(redirect(command("app"), parser.Stderr, "errors.log"))},
		{name: "numeric argument", line: `echo 2 >out`, want: plan(redirect(command("echo", "2"), parser.Output, "out"))},
		{name: "quoted descriptor argument", line: `echo "2">out`, want: plan(redirect(command("echo", "2"), parser.Output, "out"))},
		{name: "number in word", line: `echo version2>out`, want: plan(redirect(command("echo", "version2"), parser.Output, "out"))},
		{name: "leading redirection", line: `<input sort`, want: plan(redirect(command("sort"), parser.Input, "input"))},
		{name: "arguments after redirect", line: `echo >out hello`, want: plan(redirect(command("echo", "hello"), parser.Output, "out"))},
		{name: "all streams", line: `app <in >out 2>err`, want: plan(redirect(redirect(redirect(command("app"), parser.Input, "in"), parser.Output, "out"), parser.Stderr, "err"))},
		{name: "pipeline redirection", line: `cat <in 2>err | sort >>out`, want: plan(redirect(redirect(command("cat"), parser.Input, "in"), parser.Stderr, "err"), redirect(command("sort"), parser.Append, "out"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parser.Parse(tt.line)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.line, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Parse(%q) = %#v, want %#v", tt.line, got, tt.want)
			}
		})
	}
}

func TestParseInvalid(t *testing.T) {
	tests := []struct {
		line    string
		message string
	}{
		{"| echo", "missing command"},
		{"echo |", "missing command"},
		{"echo | | cat", "missing command"},
		{"> file", "missing command"},
		{"'' arg", "command name must not be empty"},
		{"echo >", "missing filename"},
		{"echo >>", "missing filename"},
		{"echo <", "missing filename"},
		{"echo 2>", "missing filename"},
		{"echo > | cat", "missing filename"},
		{"echo > > file", "missing filename"},
		{"echo >>> file", "missing filename"},
		{"echo > ''", "filename must not be empty"},
		{"echo >a >b", "multiple redirections"},
		{"echo >a >>b", "multiple redirections"},
		{"echo >>a >b", "multiple redirections"},
		{"cat <a <b", "multiple redirections"},
		{"app 2>a 2>b", "multiple redirections"},
		{"echo >out | cat", "last pipeline stage"},
		{"echo >>out | cat", "last pipeline stage"},
		{"echo | cat <in", "first pipeline stage"},
		{"echo 'hello", "unterminated quote"},
		{"echo \"hello", "unterminated quote"},
		{"echo || cat", "not supported"},
		{"echo && cat", "not supported"},
		{"echo &", "not supported"},
		{"echo ; cat", "not supported"},
		{"cat <<in", "not supported"},
		{"echo >&out", "not supported"},
		{"cat <&0", "not supported"},
		{"echo &>out", "not supported"},
		{"echo 2>>err", "not supported"},
		{"echo 2>&1", "not supported"},
		{"echo 1>out", "only the 2>"},
		{"echo 3>out", "only the 2>"},
		{"echo 12>out", "only the 2>"},
		{"echo 0<in", "only the 2>"},
		{"echo 2<in", "only the 2>"},
		{"echo\necho", "multiple command lines"},
		{"echo 'one\ntwo'", "multiple command lines"},
		{"echo \x00", "NUL bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			got, err := parser.Parse(tt.line)
			if err == nil || !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("Parse(%q) = %#v, %v; want error containing %q", tt.line, got, err, tt.message)
			}
			var syntax *parser.SyntaxError
			if !errors.As(err, &syntax) {
				t.Fatalf("error has type %T, want *SyntaxError", err)
			}
			if !reflect.DeepEqual(got, parser.Plan{}) {
				t.Fatalf("invalid input returned a partial plan: %#v", got)
			}
		})
	}
}

func TestLexOriginalSpans(t *testing.T) {
	line := ` echo "two  words"|cat 2>"error file"`
	want := []parser.Token{
		{Kind: parser.Word, Value: "echo", Start: 1, End: 5},
		{Kind: parser.Word, Value: "two  words", Start: 6, End: 18},
		{Kind: parser.Pipe, Value: "|", Start: 18, End: 19},
		{Kind: parser.Word, Value: "cat", Start: 19, End: 22},
		{Kind: parser.RedirectStderr, Value: "2>", Start: 23, End: 25},
		{Kind: parser.Word, Value: "error file", Start: 25, End: 37},
	}
	got, err := parser.Lex(line)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Lex(%q) = %#v, want %#v", line, got, want)
	}
}

func TestSyntaxErrorLocation(t *testing.T) {
	_, err := parser.Parse(`echo "unfinished`)
	var syntax *parser.SyntaxError
	if !errors.As(err, &syntax) || syntax.Offset != 5 || !strings.Contains(err.Error(), "column 6") {
		t.Fatalf("unexpected syntax error: %v", err)
	}
}

func TestValidateConstructedPlans(t *testing.T) {
	tests := []struct {
		name string
		plan parser.Plan
		bad  bool
	}{
		{name: "empty plan"},
		{name: "valid", plan: plan(command("echo", "hello"))},
		{name: "empty command", plan: plan(command("")), bad: true},
		{name: "empty target", plan: plan(redirect(command("cat"), parser.Input, "")), bad: true},
		{name: "unknown redirection", plan: plan(redirect(command("echo"), parser.Kind(255), "out")), bad: true},
		{name: "duplicate output", plan: plan(redirect(redirect(command("echo"), parser.Output, "a"), parser.Append, "b")), bad: true},
		{name: "duplicate input", plan: plan(redirect(redirect(command("cat"), parser.Input, "a"), parser.Input, "b")), bad: true},
		{name: "duplicate stderr", plan: plan(redirect(redirect(command("echo"), parser.Stderr, "a"), parser.Stderr, "b")), bad: true},
		{name: "nonfinal output", plan: plan(redirect(command("echo"), parser.Output, "out"), command("cat")), bad: true},
		{name: "nonfirst input", plan: plan(command("echo"), redirect(command("cat"), parser.Input, "in")), bad: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := parser.Validate(tt.plan); (err != nil) != tt.bad {
				t.Fatalf("Validate(%#v) = %v, want error %t", tt.plan, err, tt.bad)
			}
		})
	}
}

func FuzzParse(f *testing.F) {
	for _, seed := range []string{"", `echo "hello"`, `cat <in | sort >out`, `echo ''`, `echo \\`, `echo 2>&1`, `cat |`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, line string) {
		plan, err := parser.Parse(line)
		if err != nil {
			if len(plan.Commands) != 0 {
				t.Fatal("invalid input produced an executable partial plan")
			}
			return
		}
		for _, command := range plan.Commands {
			if command.Name == "" {
				t.Fatal("valid plan has an empty command name")
			}
		}
	})
}

func command(name string, args ...string) parser.Command {
	return parser.Command{Name: name, Args: args}
}

func redirect(command parser.Command, kind parser.Kind, target string) parser.Command {
	command.Redirections = append(command.Redirections, parser.Redirection{Kind: kind, Target: target})
	return command
}

func plan(commands ...parser.Command) parser.Plan {
	return parser.Plan{Commands: commands}
}
