# NexShell

> A structured, AI-assisted developer & automation shell.

NexShell is a modern PowerShell-inspired command shell built in Go, featuring structured data pipelines, OS process management, developer automation, extensible plugins, and a safety-aware AI assistance layer.

## Quick Start

```bash
# Prerequisites: Go 1.22+
go mod download
go build -o nexshell.exe ./cmd/nexshell
./nexshell.exe
```

## Phase 1 — Shell Core & UX

Phase 1 delivers the interactive foundation:

| Feature | Status |
|---------|--------|
| REPL with styled prompt | ✅ |
| Command registry | ✅ |
| Built-in: `cd`, `pwd`, `ls`, `cat`, `echo`, `clear` | ✅ |
| Built-in: `exit`, `help`, `env`, `history` | ✅ |
| Alias system (`alias` / `unalias`) | ✅ |
| History persistence (`~/.nexshell_history`) | ✅ |
| External command fall-through | ✅ |

## Repository Structure

```
nexshell/
├── cmd/nexshell/       # Entry point
├── internal/
│   ├── repl/           # Read-Eval-Print Loop
│   ├── registry/       # Command registry & interface
│   ├── builtins/       # Built-in commands
│   ├── history/        # Command history
│   ├── alias/          # Alias store
│   └── executor/       # External command executor
├── tests/              # Unit & integration tests
├── docs/               # Documentation
├── examples/           # Example scripts
└── nex.yaml            # Task automation config
```

## Running Tests

```bash
go test -v -race ./...
```

## Roadmap

| Phase | Focus |
|-------|-------|
| 1 ✅ | Shell Core & UX |
| 2 | Parser & Pipeline |
| 3 | OS / Process Engine |
| 4 | Automation & Plugins |
| 5 | AI, Security & Observability |
