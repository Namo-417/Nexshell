# NexShell Phase 1 — Architecture & Design Notes

## Overview

Phase 1 establishes the core shell loop, command dispatch, built-in commands,
alias resolution, and command history that every future phase builds on.

## Key Interfaces

### `registry.Command`
```go
type Command interface {
    Name() string
    Description() string
    Run(ctx Context, args []string) error
}
```
Every built-in and (eventually) every pipeline stage and plugin satisfies this
interface. The `Context` parameter provides mutable access to shell state.

### `registry.Context`
```go
type Context struct {
    CWD      *string        // current working directory (pointer so cd can mutate it)
    Aliases  AliasStore     // live alias store
    History  HistoryStore   // live history store
    Registry *Registry      // for help and meta-commands
}
```

### `alias.Store`
In-memory map with `Set / Get / Delete / All`. The `Expand` method performs
first-token replacement before command dispatch.

### `history.Store`
Thread-safe ring buffer (`sync.Mutex`) persisted to `~/.nexshell_history`.
Consecutive-duplicate suppression keeps history clean.

### `executor.Run`
Falls back to `exec.LookPath` + `exec.Command` with inherited file descriptors.
Returns typed `NotFoundError` (unknown command) or `ExecError` (non-zero exit).

## Execution Flow

```
User types input
  │
  ▼
bufio.Scanner reads line
  │
  ├─ blank / comment → skip
  │
  ├─ add to history
  │
  ├─ alias.Expand (first-token substitution)
  │
  ├─ tokenise (quote-aware split)
  │
  ├─ registry.Lookup(name)
  │    ├─ found  → cmd.Run(ctx, args)
  │    └─ not found → executor.Run(name, args)
  │
  └─ printError on non-nil error
```

## Tokeniser

The REPL tokeniser handles single-quoted and double-quoted groups, so commands
like `echo "hello world"` and `cd 'path with spaces'` work naturally:

```go
alias ll='ls -l'     // → Set("ll", "ls -l")
echo "two words"     // → args: ["two words"]
```

## Colour Scheme

| Element | Colour |
|---------|--------|
| Prompt `nexshell` label | Cyan + Bold |
| Prompt directory | Yellow |
| Directory entries (ls) | Blue |
| Executable entries (ls) | Green |
| Errors | Red |
| Exit codes | Yellow |
| Commands (help) | Green |

## File Layout

```
internal/
  registry/  registry.go          Command interface + Registry
  alias/     alias.go             Alias store
  history/   history.go           History store (persist to disk)
  builtins/  builtins.go          All 12 built-in commands
  executor/  executor.go          External command runner
  repl/      repl.go              REPL loop, prompt, tokeniser
cmd/
  nexshell/  main.go              Entry point (wiring + banner)
tests/
  registry_test.go
  alias_test.go
  history_test.go
  builtins_test.go
```

## Phase 2 Integration Points

Phase 2 (Parser & Pipeline) will:
1. Replace the simple `tokenise()` function in `repl.go` with the full
   Lexer → Parser → AST pipeline.
2. Add `|` pipe-chaining by passing Go channels between command stages.
3. Introduce `PipelineCommand` (satisfying `registry.Command`) that wraps
   multiple stages.

The `registry.Command` interface remains unchanged for built-ins.
