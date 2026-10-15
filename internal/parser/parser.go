// Package parser converts NexShell command lines into execution plans.
//
// The grammar is deliberately small: words, pipelines and file redirections.
// Quotes are removed from words, but operators inside quotes remain literal.
// Single quotes preserve every character. Outside single quotes a backslash
// escapes whitespace, quotes, backslash or an operator; before other characters
// it remains literal so ordinary Windows paths do not need extra escaping.
// Inside double quotes only backslash and double quote can be escaped.
package parser

import (
	"fmt"
	"strings"
)

// Kind identifies a file redirection.
type Kind uint8

const (
	Input  Kind = iota // < reads standard input from an existing file.
	Output             // > creates or truncates standard output's file.
	Append             // >> creates or appends to standard output's file.
	Stderr             // 2> creates or truncates standard error's file.
)

// Redirection attaches a file to one command stream.
type Redirection struct {
	Kind   Kind
	Target string
}

// Command is a command name, its arguments and its stream redirections.
type Command struct {
	Name         string
	Args         []string
	Redirections []Redirection
}

// Plan is a foreground pipeline. An empty command line yields an empty plan.
type Plan struct {
	Commands []Command
}

// TokenKind identifies a word or supported operator in a command line.
type TokenKind uint8

const (
	Word TokenKind = iota
	Pipe
	RedirectInput
	RedirectOutput
	RedirectAppend
	RedirectStderr
)

// Token contains a decoded value and its half-open byte span in the input.
// Keeping the span allows callers to inspect original spelling without joining
// decoded arguments and accidentally changing whitespace or quoting.
type Token struct {
	Kind       TokenKind
	Value      string
	Start, End int
}

// SyntaxError reports the byte offset at which malformed input was detected.
type SyntaxError struct {
	Offset  int
	Message string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("parse: %s at column %d", e.Message, e.Offset+1)
}

func syntax(offset int, message string) error {
	return &SyntaxError{Offset: offset, Message: message}
}

// Lex splits input into words and operators without executing anything.
// Empty quoted words are retained. There is no interpolation or expansion.
func Lex(input string) ([]Token, error) {
	var tokens []Token
	var word strings.Builder
	started := false
	start := 0
	quote := byte(0)
	quoteStart := 0

	flush := func(end int) {
		if started {
			tokens = append(tokens, Token{Kind: Word, Value: word.String(), Start: start, End: end})
			word.Reset()
			started = false
		}
	}
	begin := func(at int) {
		if !started {
			start = at
			started = true
		}
	}
	operator := func(kind TokenKind, at, length int) {
		flush(at)
		tokens = append(tokens, Token{Kind: kind, Value: input[at : at+length], Start: at, End: at + length})
	}

	for i := 0; i < len(input); i++ {
		ch := input[i]
		if ch == 0 {
			return nil, syntax(i, "NUL bytes are not supported")
		}
		if ch == '\r' || ch == '\n' {
			return nil, syntax(i, "multiple command lines are not supported")
		}
		if quote == '\'' {
			if ch == '\'' {
				quote = 0
			} else {
				word.WriteByte(ch)
			}
			continue
		}
		if quote == '"' {
			switch {
			case ch == '"':
				quote = 0
			case ch == '\\' && i+1 < len(input) && (input[i+1] == '"' || input[i+1] == '\\'):
				i++
				word.WriteByte(input[i])
			default:
				word.WriteByte(ch)
			}
			continue
		}

		// Descriptor prefixes are operators only at an unquoted word boundary.
		// Thus `echo 2 > file` keeps 2 as an argument, while `2>file` redirects.
		if !started && ch >= '0' && ch <= '9' {
			end := i + 1
			for end < len(input) && input[end] >= '0' && input[end] <= '9' {
				end++
			}
			if end < len(input) && (input[end] == '<' || input[end] == '>') {
				if input[i:end] != "2" || input[end] != '>' {
					return nil, syntax(i, "only the 2> descriptor redirection is supported")
				}
				if end+1 < len(input) && (input[end+1] == '>' || input[end+1] == '&') {
					return nil, syntax(i, "stderr append and descriptor duplication are not supported")
				}
				operator(RedirectStderr, i, end-i+1)
				i = end
				continue
			}
		}

		switch ch {
		case ' ', '\t':
			flush(i)
		case '\'', '"':
			begin(i)
			quote, quoteStart = ch, i
		case '\\':
			begin(i)
			if i+1 < len(input) && strings.ContainsRune(" \t'\"\\|<>&;", rune(input[i+1])) {
				i++
				word.WriteByte(input[i])
			} else {
				word.WriteByte(ch)
			}
		case '|':
			if i+1 < len(input) && input[i+1] == '|' {
				return nil, syntax(i, "logical operator || is not supported")
			}
			operator(Pipe, i, 1)
		case '<':
			if i+1 < len(input) && (input[i+1] == '<' || input[i+1] == '&') {
				return nil, syntax(i, "heredocs and descriptor duplication are not supported")
			}
			operator(RedirectInput, i, 1)
		case '>':
			if i+1 < len(input) && input[i+1] == '&' {
				return nil, syntax(i, "descriptor duplication is not supported")
			}
			if i+1 < len(input) && input[i+1] == '>' {
				operator(RedirectAppend, i, 2)
				i++
			} else {
				operator(RedirectOutput, i, 1)
			}
		case '&', ';':
			return nil, syntax(i, fmt.Sprintf("operator %q is not supported", string(ch)))
		default:
			begin(i)
			word.WriteByte(ch)
		}
	}
	if quote != 0 {
		return nil, syntax(quoteStart, "unterminated quote")
	}
	flush(len(input))
	return tokens, nil
}

// Parse validates a complete command line and returns its execution plan.
// A stream can have at most one redirection per command. A pipeline's input
// redirection belongs on its first stage and output redirection on its last;
// competing pipe/file connections are rejected rather than silently overridden.
func Parse(input string) (Plan, error) {
	tokens, err := Lex(input)
	if err != nil {
		return Plan{}, err
	}
	if len(tokens) == 0 {
		return Plan{}, nil
	}
	var plan Plan
	var command Command
	seen := make(map[Kind]bool)
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch token.Kind {
		case Word:
			if command.Name == "" {
				if token.Value == "" {
					return Plan{}, syntax(token.Start, "command name must not be empty")
				}
				command.Name = token.Value
			} else {
				command.Args = append(command.Args, token.Value)
			}
		case Pipe:
			if command.Name == "" {
				return Plan{}, syntax(token.Start, "missing command before pipe")
			}
			plan.Commands = append(plan.Commands, command)
			command = Command{}
			seen = make(map[Kind]bool)
		default:
			kind := redirectionKind(token.Kind)
			stream := kind
			if kind == Append {
				stream = Output
			}
			if seen[stream] {
				return Plan{}, syntax(token.Start, "multiple redirections for the same stream are not supported")
			}
			seen[stream] = true
			if i+1 >= len(tokens) || tokens[i+1].Kind != Word {
				return Plan{}, syntax(token.Start, fmt.Sprintf("missing filename after %s", token.Value))
			}
			i++
			if tokens[i].Value == "" {
				return Plan{}, syntax(tokens[i].Start, "redirection filename must not be empty")
			}
			command.Redirections = append(command.Redirections, Redirection{Kind: kind, Target: tokens[i].Value})
		}
	}
	if command.Name == "" {
		return Plan{}, syntax(len(input), "missing command")
	}
	plan.Commands = append(plan.Commands, command)
	if err := Validate(plan); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

// Validate checks a plan constructed by a caller, including plans assembled
// during alias expansion. It applies the same stream rules as Parse. The empty
// plan is valid and requires no execution.
func Validate(plan Plan) error {
	for stage, command := range plan.Commands {
		if command.Name == "" {
			return syntax(0, "command name must not be empty")
		}
		seen := make(map[Kind]bool)
		for _, redirect := range command.Redirections {
			if redirect.Kind > Stderr {
				return syntax(0, "unsupported redirection kind")
			}
			if redirect.Target == "" {
				return syntax(0, "redirection filename must not be empty")
			}
			stream := redirect.Kind
			if stream == Append {
				stream = Output
			}
			if seen[stream] {
				return syntax(0, "multiple redirections for the same stream are not supported")
			}
			seen[stream] = true
			if redirect.Kind == Input && stage != 0 {
				return syntax(0, "input redirection is only supported on the first pipeline stage")
			}
			if (redirect.Kind == Output || redirect.Kind == Append) && stage != len(plan.Commands)-1 {
				return syntax(0, "output redirection is only supported on the last pipeline stage")
			}
		}
	}
	return nil
}

func redirectionKind(token TokenKind) Kind {
	switch token {
	case RedirectInput:
		return Input
	case RedirectOutput:
		return Output
	case RedirectAppend:
		return Append
	default:
		return Stderr
	}
}
