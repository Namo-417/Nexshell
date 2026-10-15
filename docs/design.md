# NexShell — Architecture, Audit and Phase 2 Design

## Repository audit (16 October 2026)

The original repository contained one Go module (`nexshell`, Go 1.22), no external dependencies, one entry point, six internal packages, package-local tests, this design document, README, `nex.yaml`, and a tracked Windows executable. There was no separate parser package or execution plan, pipeline support, redirection support, or executor/REPL test coverage.

The entry point registered twelve built-ins and constructed alias/history stores. `registry.Command.Run(Context, []string) error` dispatched built-ins; `Context` carried CWD, aliases, history and registry. Built-ins used global stdin/stdout/stderr, `cd` changed the process directory, and `exit` called `os.Exit`. The REPL used a `bufio.Scanner`, a private quote-toggle tokenizer and first-token alias substitution. External execution used `exec.LookPath` and `exec.Command` with global streams and inherited environment/directory. History held up to 1,000 entries with mutex protection and snapshot persistence.

Baseline commands were run before editing source on Windows amd64, Go 1.26.4:

| Command | Baseline result |
|---|---|
| `go mod download` | Passed; no module dependencies |
| `go build ./...` | Passed |
| `go build -o <temporary-path>/nexshell-baseline.exe ./cmd/nexshell` | Passed; equivalent entry-point build without replacing the tracked binary |
| `go test ./...` | Failed in builtins: `TestCd_ValidDir` restored a directory captured after `cd`, preventing Windows temporary-directory cleanup; `TestCd_NoArgs_GoesToHome` accessed the real home directory, denied by the sandbox. Alias/history/registry passed. |
| `go vet ./...` | Passed |
| `go test -v -race ./...` | Failed before tests: MinGW GCC could not start (`Illegal System DLL Relocation`) |

Additional audited limitations: empty quoted arguments were dropped; tabs did not delimit words; malformed quotes were accepted; alias expansion collapsed quoted whitespace and alias creation removed intentional executable quotes. Scanner read-ahead could consume child input, and its 64 KiB limit ended the shell silently. Built-in file errors did not consistently return failure; ignored save errors and direct `os.Exit` prevented reliable cleanup. The implementation addresses the Phase 2 integration issues and preserves the bounded history/alias architecture. Cross-session history merging remains outside this phase.

## Package boundaries and execution flow

```text
REPL reads exactly one line
  -> parser.Parse (Lex -> Plan -> Validate)
  -> one-level, per-stage alias expansion
  -> parser.Validate expanded Plan
  -> executor.Execute
       validate/preflight command lookup and pipeline safety
       open redirection files and connect OS pipes
       start/track external children
       run safe built-ins concurrently
       collect every result, close resources, compute status
  -> next prompt, or clean ExitError/EOF handling
```

`internal/parser` contains the lexer, `Plan`, `Command`, `Redirection`, `SyntaxError`, and shared plan validation. `Lex` also exposes original byte spans. Parsing has no process or filesystem side effects. The previous private tokenizer could not represent operators or return syntax errors; replacing it with this focused package avoids mixing syntax with execution.

`internal/registry` retains the existing command interface. Its additive context fields carry `context.Context`, `io.Reader` stdin, and `io.Writer` stdout/stderr. Helpers fall back to original process streams for old direct callers. `PipelineCommand.PipelineSafe()` is an optional capability; undeclared commands are conservatively rejected in pipelines. `ExitError` requests shell termination without terminating inside a built-in.

`internal/builtins` writes exclusively through supplied streams, resolves relative paths against the context directory, and propagates I/O errors. Stream-aware `cat` reads explicit input when no file operands are supplied, and returns accumulated missing-file failures. Native unredirected `cat` preserves the Phase 1 missing-operand error. `cd` still calls `os.Chdir` and updates the parent context; stateful built-ins cannot run in pipelines.

`internal/executor` is the single execution implementation; the old `Run(name,args)` delegates into it. Direct callers can inspect typed `NotFoundError`, `StartError`, `RedirectionError`, and `ExecError`. The executor owns stage diagnostics; the REPL does not print returned execution errors a second time. Per-execution writer synchronization makes shared custom writers safe when multiple stages report errors. Caller-owned standard streams remain open.

`internal/repl` owns prompt output, signal registration, history lifecycle, parsing and alias expansion. Its one-byte-at-a-time line reader avoids read-ahead into subprocess input and accepts lines larger than 64 KiB. A read is requested only while idle; there is no competing reader during foreground execution. The foreground execution finishes before shell state is read for the next prompt. Signal registration is removed when the shell returns. `main` owns `os.Exit` after cleanup.

## Grammar and semantics

The supported grammar is a sequence of words/redirections forming one command, followed by zero or more `| command` stages. Spaces and tabs delimit words; single quotes are literal; double quotes support only escaped quote/backslash; outside quotes a backslash escapes whitespace, quote, backslash or operator. Other backslashes stay literal to retain ordinary Windows paths. Empty words and adjacent quote groups are preserved. No interpolation, substitutions, globbing, scripting or multiline grammar is introduced.

A full line starting with `#` after whitespace is skipped by the REPL. Quoted operators are literal arguments. Unsupported operators and malformed syntax are rejected before execution. The README provides examples and the exact escaping rules.

Each stage has at most one redirection per stream. `>` and `>>` conflict with each other. `<` is allowed on the first stage, `>`/`>>` on the last, and `2>` on any stage. These restrictions reject ambiguous pipe/file connections explicitly. Redirections are processed in their listed order. Different streams may target the same pathname, but independent file offsets do not provide descriptor duplication; use distinct files for predictable separate output.

Redirection targets and cat operands are limited to regular files, including symlinks that resolve to regular files. Existing directories, devices and named pipes are rejected with clear errors before opening; advanced device/FIFO I/O is not implemented. Output files use create/write/truncate or create/write/append flags and mode 0666 subject to the OS umask/ACL; input files must exist. The executor owns and closes opened files. Lookup/safety checks run before output files are opened; preflight diagnostics honor the failing stage's stderr redirection where it can be opened. Redirection failures do not crash the REPL. Standard output is never used for shell execution errors.

Pipelines carry bytes through OS pipes. All children start before the executor waits for completion; built-ins run concurrently. Unneeded parent pipe handles close after child inheritance, and built-in handles close when that stage returns. Partial startup failure stops and waits for every already-started child. Ordinary completed nonzero stages use pipefail rather than forcing early cancellation of other stages; each stage must finish or be interrupted.

The status is 0 on success; otherwise the rightmost nonzero command stage wins. Syntax/invalid-plan/unsafe-pipeline errors use 2; I/O/built-in failure 1; startup failure 126; lookup failure 127; cancellation 130. External exit codes are retained and their nonzero status is reported once through that stage's stderr; a Unix terminating signal maps to 128 + its number. A broken pipe is a real failing stage under this policy. EOF returns the last status; `exit [code]` defaults to 0 and validates the range 0–255.

Aliases expand once per original stage. An alias value can itself contain a pipeline. Invocation arguments attach to its final stage, input redirection to its first, and output/stderr redirection to its final stage. Expanded plans go through shared validation. Aliases remain session-only; history records the original invocation.

## Process lifecycle and platform handling

Shared execution logic uses `os/exec`, `os.Pipe`, and context cancellation. OS-specific tracking is isolated in process files. Windows starts each child suspended, assigns it to a kill-on-close Job Object, then resumes its initial thread. This prevents a spawn-before-assignment race. Assignment/resume failure kills the suspended child and waits for it. Cancellation terminates the job, and closing it removes descendants that survived direct parent completion. Windows 8+ nested-job support is required; a restrictive host job can cause a reported startup error.

Linux, macOS, and BSD use one process group for all external stages. The first child hands the original shell input terminal to the group before exec; cleanup restores its previous foreground group with SIGTTOU temporarily ignored. Direct children are not reaped until startup completes, retaining a fast-exiting group leader while subsequent stages join. A child terminated by SIGINT cancels the execution context, closes owned streams, and kills the remainder of the group. Descendants that deliberately detach into another process group/session can escape this Unix mechanism. AIX/Solaris reject inherited terminal input explicitly; their file/pipe path remains unverified. Other OS targets report unsupported foreground process management. Linux runtime tests now pass under Ubuntu 24.04 on WSL2, including real pseudo-terminal input, terminal Ctrl+C, foreground restoration and descendant cleanup. macOS/BSD runtime behavior remains unverified; macOS cross-compilation passes and hosted CI is prepared locally. Full background job control, `fg`, and `bg` are intentionally deferred.

Arbitrary custom readers/writers and third-party built-ins must cooperate with cancellation. Go cannot forcibly stop an arbitrary blocked interface method without closing caller-owned resources or leaking a goroutine; the executor does neither. Engine-owned pipe handles are closed on interruption. Native standard streams remain owned by the shell. Tests supply finite readers or explicit readiness/cancellation handshakes.

History uses the original mutex-protected store, retaining 1,000 entries and consecutive-duplicate suppression. Long command lines also round-trip through history without Scanner limits. New Unix history files request 0600; existing permissions are preserved. Save errors and close failures are surfaced. Two sessions can still overwrite each other's history snapshots, which is documented rather than silently claimed fixed.

## Files changed

Created:

- `internal/parser/parser.go`, `internal/parser/parser_test.go`
- `internal/executor/executor_test.go`, `process_windows.go`, `process_unix.go`, `process_other.go`, `terminal_unix.go`, `terminal_libc.go`, `process_windows_test.go`, `process_unix_test.go`, `process_interrupt_unix_test.go` (all under `internal/executor`)
- `internal/repl/repl_test.go`, `input_windows.go`, `input_other.go`, `input_windows_test.go` (all under `internal/repl`)

Modified:

- `cmd/nexshell/main.go`
- `internal/registry/registry.go` and `registry_test.go`
- `internal/builtins/builtins.go` and `builtins_test.go`
- `internal/executor/executor.go`
- `internal/repl/repl.go`
- `internal/alias/alias.go` and `alias_test.go`
- `internal/history/history.go` and `history_test.go`
- `README.md`, `docs/design.md`, and the rebuilt `nexshell.exe`

No source or test files were removed; `go.mod` and `nex.yaml` remain unchanged. No dependency was added. The original command interface and entry-point wiring remain; changes to stream context and exit control were necessary to implement concurrent/redirection-safe execution without global stream replacement or premature termination.

## Verification strategy

Parser tests are table-driven and include quote groups, Unicode, empty arguments, escaping, Windows paths, every operator, invalid combinations, missing operands, direct AST validation, token spans and fuzz seeds. Executor tests use portable Go subprocess fixtures with explicit timeouts; no dependency on `grep`, Unix `sort`, or other platform tools is required. Built-in regressions capture actual output and errors; existing tests are retained, with temporary home/directory setup corrected. REPL tests cover parser recovery, aliases, long lines, unbuffered subprocess input, redirected pipelines, exit/history behavior, injected Ctrl+C cancellation, idle interrupts, and simulated Windows ReadConsole-aborted error recovery. A physical interactive console Ctrl+C event was not synthesized.

Runtime verification is performed on Windows amd64 and the existing Ubuntu 24.04 WSL2 environment. Initial WSL queries and compiler invocation failed inside the execution sandbox; approved execution outside that sandbox found the existing distro. A project-local verified 64-bit compiler resolved Windows race testing, and isolated Linux Go/GCC toolchains enabled native Linux tests and race detection without installing distro packages globally. Cross-compilation and unrun macOS CI remain distinguished from executed native tests.

## Final verification results

Verified native platforms: Windows amd64 and Ubuntu 24.04 WSL2 Linux amd64, Go 1.26.4. All seven internal package test suites and race suites passed on both platforms. The entry point has no package-local test file and was exercised through native binary smoke tests.

| Command/check | Actual final result |
|---|---|
| `go test ./...` | Passed, exit 0: alias, builtins, executor, history, parser, registry, repl |
| `go test -timeout 120s ./...` | Passed during integration |
| `go build ./...` | Passed, exit 0 |
| `go build -o nexshell.exe ./cmd/nexshell` | Passed, exit 0; tracked Windows executable rebuilt for Phase 2 |
| `go vet ./...` | Passed, exit 0 |
| `scripts/test-race.ps1` (Windows) | Passed, exit 0: `go test -race -count=1 -timeout=5m ./...` with portable x64 LLVM-MinGW/Clang 23.1.1. No races reported in any of seven packages. |
| Native Linux tests | Passed, exit 0: `CGO_ENABLED=0 go test -count=1 -timeout=120s ./...` with isolated Go 1.26.4 in Ubuntu 24.04 WSL2. |
| Native Linux race tests | Passed, exit 0: `CGO_ENABLED=1 CC=<project>/.tools/linux/gcc go test -race -count=1 -timeout=180s ./...`; isolated GCC 13.3.0; no races reported. |
| Native Linux terminal tests | Passed: new `TestLinuxForegroundTerminal` subtests for read/EOF and terminal-generated Ctrl+C, plus the actual REPL smoke driver. Foreground restoration and descendant cleanup asserted. |
| Native Linux build/vet | Passed with isolated Go 1.26.4. |
| Linux amd64 full cross-build | Passed, exit 0 with `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./...` |
| macOS amd64 full cross-build | Passed, exit 0 with `GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build ./...` |
| Linux/macOS executor test cross-compilation | Passed, exit 0 using `go test -c -o <temporary-test-binary> ./internal/executor` under the corresponding GOOS/GOARCH/CGO_ENABLED settings; included Unix SIGINT tests; temporary binaries removed |
| Windows binary smoke | Passed, exit 0: overwrite/append, `<`, built-in pipeline, `2>`, error recovery, clean exit and isolated history |
| `gofmt -l .` and `git diff --check` | Passed |

The cross-build environment was set in child PowerShell sessions as follows (no persistent project configuration changed):

```powershell
$env:CGO_ENABLED = '0'
$env:GOARCH = 'amd64'
$env:GOOS = 'linux'
go build ./...
$env:GOOS = 'darwin'
go build ./...
```

The binary smoke ran in a fresh temporary directory with that directory supplied as the child home, using these input lines:

```text
echo zebra > output.txt
echo alpha >> output.txt
cat < output.txt | cat > copy.txt
cat missing.txt 2> errors.log
echo RECOVERED
exit 0
```

Assertions verified exact copied contents, the redirected missing-file diagnostic, empty inherited stderr, recovery output, and exit status 0. Automated subprocess fixtures also verify successful external execution, silent/non-silent nonzero exits, PATH/environment/CWD inheritance, two/three-stage pipelines, 2 MiB draining, partial startup failure, missing input, overwrite/append/create, per-stage stderr, rightmost-nonzero status, process-tree cleanup, and mixed built-in/external cancellation. The original cd failures are resolved with correct cleanup and temporary-home setup; no original test was deleted or weakened.

Remaining verification scope: native macOS/BSD tests have not run. Linux terminal handoff and terminal-generated Ctrl+C are now verified using a real PTY. Physical Windows console Ctrl+C was not synthesized; signal delivery is injected in REPL tests, console-read aborts are simulated, and native process cancellation/descendant cleanup is tested through contexts. No known failing Windows/Linux native unit, integration or race test remains. All changes remain local as requested; no branch, PR, or hosted CI run was published.

## Reproducing the verification setup

Windows local setup:

```powershell
.\scripts\install-race-toolchain.ps1
.\scripts\test-race.ps1
```

The installer pins the official [LLVM-MinGW 20260908 release](https://github.com/mstorsjo/llvm-mingw/releases/tag/20260908), asset `llvm-mingw-20260908-ucrt-x86_64.zip`, with SHA-256 `1bcf74d06b724aeecaa6412ca85f5b26fb1da770e7cdcefa9263c9c5c3ad34b6`. It verifies downloaded/cached archives before extraction and reuses a complete installation without overwriting it. The race script validates the x64 target and `libsynchronization.a`, selects the compiler through process-local CC/PATH/CGO_ENABLED, and restores previous values in `finally`. This follows [Go's race compiler requirements](https://go.dev/doc/articles/race_detector#Requirements). The existing 32-bit system GCC and persistent user/system configuration remain unchanged. `.tools/` and `.verification/` are ignored by Git.

The first local LLVM invocation inside the sandbox compiled C successfully but the sandbox denied the linker permission to create test executables. Running the same script outside that sandbox passed. This is distinct from the original incompatible-GCC failure.

Linux verification used the existing Ubuntu-24.04 distro, a checksum-verified Go 1.26.4 archive, and privately extracted GCC 13 Ubuntu packages from signed package metadata. No global apt package installation or distro configuration change was made. The exact successful race invocation was:

```sh
cd /mnt/c/Documents/NexShell
GOTOOLCHAIN=local CGO_ENABLED=1 \
  CC=/mnt/c/Documents/NexShell/.tools/linux/gcc \
  GOCACHE=/tmp/nexshell-phase2-gocache \
  /tmp/nexshell-phase2-tools/go/bin/go test -race -count=1 -timeout=180s ./...
```

The initial temporary Go extraction was used for that completed run. A persistent copy is available at `.tools/linux/go/bin/go`; the private GCC wrapper at `.tools/linux/gcc` selects its sysroot under `.tools/linux/cc`. A regular Linux machine with installed Go and a compatible C compiler can simply run `go test -race ./...`.

To exercise the actual Unix REPL with a controlling terminal:

```sh
go build -o /tmp/nexshell ./cmd/nexshell
python3 scripts/verify-unix-terminal.py /tmp/nexshell
```

The driver uses Python 3.9+ standard-library POSIX PTY APIs and `/bin/cat`/`/bin/sleep`, isolates HOME, verifies stdin/EOF, foreground pipeline Ctrl+C cleanup, restored prompt/terminal ownership, idle Ctrl+C and clean exit, and reaps processes on failure. It passed on Linux. It is prepared for macOS but has not been run there.

`.github/workflows/verify.yml` provides native Windows/Linux/macOS jobs using Go from `go.mod`, uncached race tests with a five-minute timeout, build, vet, and the Unix terminal driver on Linux/macOS. Windows selects and validates the runner's x64 MinGW compiler. The workflow and helper scripts are local changes; the user requested that nothing be pushed, so no macOS hosted test result is claimed.

Additional files for this verification follow-up: `.gitignore`, `.github/workflows/verify.yml`, `scripts/install-race-toolchain.ps1`, `scripts/test-race.ps1`, `scripts/verify-unix-terminal.py`, and `internal/executor/process_terminal_linux_test.go`. Production Go code did not require changes after the new native/race tests.

## Phase 3 direction

The next phase should build on the foreground executor with a job table, background process groups, terminal/job-control integration, suspend/resume semantics, process inspection, and CI coverage on real Windows and Unix hosts. Cross-process history merging is a separate focused improvement. Do not add background execution by interpreting `&` until lifecycle and UX semantics are designed and tested.
