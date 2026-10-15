# NexShell

> A structured, AI-assisted developer & automation shell.

NexShell is a PowerShell-inspired shell written in Go. Phase 1 provides its interactive foundation; Phase 2 adds a foreground command execution engine with parsed pipelines and file redirection. AI assistance, plugins, task execution, and background jobs remain roadmap items.

## Build and run

Go 1.22 or newer is required. No third-party Go dependencies are used.

```sh
go mod download
go build -o nexshell.exe ./cmd/nexshell
./nexshell.exe
```

The `.exe` output name is retained from the original project; a Unix build can use `go build -o nexshell ./cmd/nexshell`. `nex.yaml` records build, test, lint, clean, and development commands; NexShell does not execute task files yet.

## Command syntax

```sh
pwd
cd 'path with spaces'
echo "hello world"
ls | sort
cat app.log | grep ERROR | sort
ls > files.txt
echo hello >> output.txt
sort < input.txt
./app 2> errors.log
cat < output.txt
cat missing.txt 2> errors.log
cat app.log | grep ERROR > errors.txt
```

External commands such as `sort`, `grep`, and `./app` must exist on the current platform. Tests use portable Go subprocess fixtures rather than assuming these programs are installed.

The built-in registry contains `cd`, `pwd`, `ls`, `cat`, `echo`, `clear`, `exit`, `help`, `env`, `history`, `alias`, and `unalias`. Relative built-in paths and redirection targets use the shell working directory. External programs inherit the environment and run in that directory.

### Quoting and escaping

- Spaces and tabs separate words. Quoted empty arguments are preserved. Adjacent quoted and unquoted parts form one argument.
- Single quotes preserve everything between them literally; they cannot contain a single quote.
- Double quotes preserve spaces and operators. Within double quotes, `\"` represents a literal double quote and `\\` a literal backslash; other backslashes remain literal.
- Outside quotes, backslash escapes a space, tab, quote, backslash, or operator (`|`, `<`, `>`, `&`, `;`). Before other characters, or at the end of a word, a backslash is literal. Ordinary paths such as `C:\Users\name` therefore work. Single-quote UNC paths and paths ending in a backslash, for example `'\\server\share'` and `'C:\folder\'`.
- There is no variable interpolation, command substitution, glob expansion, inline-comment grammar, or multiline syntax. A line whose first non-whitespace character is `#` is a comment.
- Missing commands or filenames, empty command names, unterminated quotes, and unsupported operators produce a parse error before any stage runs.

### Pipelines and redirection

All stages are parsed and validated before execution. Stages run concurrently and carry byte streams, not structured objects. Each stdout connects to the next stdin; stderr stays separate unless redirected. NexShell waits for every started child and closes owned pipe ends.

| Form | Behavior |
|---|---|
| `< file` | Read an existing file as stdin |
| `> file` | Create or truncate stdout's file |
| `>> file` | Create or append to stdout's file |
| `2> file` | Create or truncate stderr's file |

One redirection per stream is allowed on each stage; `>` and `>>` count as the same stream. Input redirection belongs on the first pipeline stage and output redirection on the last. Competing pipe/file connections and duplicate stream redirections are rejected explicitly. Stderr may be redirected on any stage. Filenames can be quoted; operators need no surrounding spaces. Redirection targets and `cat` file operands must be regular files (symlinks to regular files are accepted); directories, devices, and named pipes are rejected rather than risking uninterruptible I/O.

Safe output built-ins and `cat` can run in pipelines. `cd`, `exit`, `alias`, and `unalias` are rejected inside pipelines, so they cannot change parent state unexpectedly. Standalone `cd` changes the shell directory; standalone `exit [0-255]` returns through the REPL so resource cleanup and history saving finish before termination. Native standalone `cat` retains its missing-operand error unless input is explicitly supplied by `<` or a pipe. `cat` continues over missing file operands but returns failure.

### Aliases and history

```sh
alias say='echo'
say "a  b"
alias errors='cat app.log | grep ERROR'
errors > errors.txt
unalias say
history
```

Aliases are session-local and expand once per original pipeline stage. Their values are parsed with the same grammar, preserving argument contents and embedded executable quotes. Invocation arguments are appended to the alias's final stage; invocation input redirection attaches to its first stage, and output/stderr redirection to its final stage. The expanded plan is validated again. Recursive alias expansion is not supported.

History records original command lines, suppresses consecutive duplicates, and keeps the latest 1,000 entries in `~/.nexshell_history`. Save failures are reported. New history files use private permissions on Unix. Existing history files keep their existing permissions. Concurrent shell sessions still save independent history snapshots: the last save wins; cross-process history merging is a known Phase 1 limitation.

### Exit status and errors

Pipelines use **pipefail**: success is 0; otherwise the rightmost failing stage supplies the exit status. An upstream failure is therefore visible even if the final stage succeeds. Expected broken-pipe failures are not suppressed. EOF returns the last command status; `exit` without an argument explicitly returns 0.

| Status | Meaning |
|---|---|
| 0 | Success |
| 1 | Built-in or redirection/I/O failure |
| 2 | Syntax error, invalid plan, or unsafe built-in in a pipeline |
| 126 | Located executable could not start, or process tracking failed |
| 127 | Command not found |
| 130 | Foreground interruption or context cancellation |
| Other | External program exit code; Unix signals use 128 + signal number |

Execution diagnostics are written once through the relevant stderr stream. External programs retain their own stderr output; NexShell also reports a nonzero external exit status through that stage's stderr. Parse and alias errors are reported by the REPL. Failed commands leave the shell available for the next command.

### Interruption and platform scope

Ctrl+C cancels the active foreground pipeline and the next prompt appears after cleanup. At an idle prompt it keeps the session alive. Windows children start suspended, join a kill-on-close Job Object, and resume only after tracking is established. Cancellation and partial startup failures terminate tracked children; closing the job also removes descendants that outlive their direct command. A host that disallows nested job assignment produces a clear startup error rather than running an untracked child.

On Linux and BSD, external stages share a process group with foreground terminal handoff and restoration. Cancellation kills that group; descendants that deliberately create another group or session can escape it. Linux runtime behavior is verified in Ubuntu 24.04 on WSL2, including real terminal input, Ctrl+C, foreground restoration, and descendant cleanup. Windows and Linux race tests pass. BSD runtime behavior remains unverified. AIX/Solaris terminal input is explicitly unsupported; their nonterminal process-group path is unverified. Other operating systems report unsupported process management. See [architecture and verification notes](docs/design.md) for the precise platform behavior and recorded results. Full job control, background jobs, `fg`, and `bg` are outside Phase 2.

The stream API accepts custom readers/writers. Such implementations and custom built-ins must cooperate with cancellation and unblock pending I/O; Go cannot safely force an arbitrary user-supplied `Read`, `Write`, or command function to return. The engine never closes the shell's inherited standard streams.

## Test and verification

```sh
go test ./...
go test -race ./...
go build ./...
go vet ./...
```

The task definition's verbose test command is `go test -v -race ./...`. The race detector needs a working supported C compiler. Tests use temporary files/directories and explicit subprocess timeouts, and do not read or modify the user's actual history/home files. Detailed baseline and final results are recorded in [docs/design.md](docs/design.md).

On Windows, use the verified project-local 64-bit compiler instead of the older 32-bit GCC on this machine:

```powershell
.\scripts\install-race-toolchain.ps1
.\scripts\test-race.ps1
```

The installer downloads pinned [LLVM-MinGW 20260908](https://github.com/mstorsjo/llvm-mingw/releases/tag/20260908), verifies its SHA-256, and extracts it into ignored `.tools/`. The test script selects that compiler for its process and restores the previous environment afterward. Neither script changes the system compiler, user PATH, or persistent Go settings. An existing compatible compiler can be selected with `-CompilerPath`. Inside a restricted execution sandbox, run the race script in an ordinary terminal if the linker is denied permission to create executables.

On a Unix host with Go and a C compiler, run the commands above directly. To exercise the actual interactive shell with a pseudo-terminal (Python 3.9+):

```sh
go build -o /tmp/nexshell ./cmd/nexshell
python3 scripts/verify-unix-terminal.py /tmp/nexshell
```

The native CI matrix in `.github/workflows/verify.yml` runs uncached race tests, builds, and vet on Windows and Linux, plus the Unix terminal smoke test on Linux.

## Repository structure

```text
cmd/nexshell/       entry point and welcome banner
internal/parser/    lexer, structured plans, validation
internal/executor/  concurrent execution and OS process cleanup
internal/repl/      prompt, signals, aliases, history lifecycle
internal/registry/  command interface, state and stream context
internal/builtins/  built-in commands and regression tests
internal/alias/     session alias store
internal/history/   bounded persistent history
docs/design.md      architecture, audit and verification
nex.yaml            task command definitions
```

## Roadmap

| Phase | Focus |
|---|---|
| 1 | Shell core and UX |
| 2 | Parsing, foreground pipelines, redirection and process cleanup |
| 3 | Deeper OS/process integration and job control |
| 4 | Automation and plugins |
| 5 | AI, security and observability |

Unsupported syntax includes `&&`, `||`, `;`, background `&`, heredocs, descriptor duplication, descriptors other than `2>`, stderr append, substitutions, interpolation, globbing, loops, and functions. Quote operator characters when passing them literally.
