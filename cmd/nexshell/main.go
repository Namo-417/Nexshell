// Command nexshell is the entry point for the NexShell interactive shell.
// It wires up the command registry, alias store, history, registers all
// built-in commands, and starts the REPL or executes commands directly (-c).
package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"nexshell/internal/alias"
	"nexshell/internal/builtins"
	"nexshell/internal/executor"
	"nexshell/internal/history"
	"nexshell/internal/parser"
	"nexshell/internal/registry"
	"nexshell/internal/repl"
)

const banner = `
  ███╗   ██╗███████╗██╗  ██╗███████╗██╗  ██╗███████╗██╗     ██╗
  ████╗  ██║██╔════╝╚██╗██╔╝██╔════╝██║  ██║██╔════╝██║     ██║
  ██╔██╗ ██║█████╗   ╚███╔╝ ███████╗███████║█████╗  ██║     ██║
  ██║╚██╗██║██╔══╝   ██╔██╗ ╚════██║██╔══██║██╔══╝  ██║     ██║
  ██║ ╚████║███████╗██╔╝ ██╗███████║██║  ██║███████╗███████╗███████╗
  ╚═╝  ╚═══╝╚══════╝╚═╝  ╚═╝╚══════╝╚═╝  ╚═╝╚══════╝╚══════╝╚══════╝
`

func main() {
	// Build registry, aliases, and history.
	reg := registry.New()
	aliases := alias.New()
	hist := history.New()

	// Register all built-in commands (Members 1, 2, 3, 4, 5).
	builtins.RegisterAll(reg)

	// Create shell instance.
	shell, err := repl.New(reg, aliases, hist)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexshell: fatal: %v\n", err)
		os.Exit(1)
	}

	// Support one-shot command execution via -c flag: nexshell -c "command"
	if len(os.Args) >= 3 && os.Args[1] == "-c" {
		cmdLine := strings.Join(os.Args[2:], " ")
		plan, err := parser.Parse(cmdLine)
		if err != nil {
			fmt.Fprintf(os.Stderr, "nexshell: parse error: %v\n", err)
			os.Exit(2)
		}
		cwd, _ := os.Getwd()
		ctx := registry.Context{
			CWD:      &cwd,
			Aliases:  aliases,
			History:  hist,
			Registry: reg,
			Stdin:    os.Stdin,
			Stdout:   os.Stdout,
			Stderr:   os.Stderr,
		}
		status, _ := executor.Execute(context.Background(), plan, ctx)
		os.Exit(status)
	}

	// Print welcome banner in interactive mode.
	fmt.Print("\033[36m" + banner + "\033[0m")
	fmt.Printf("  NexShell v1.0.0  •  Structured, AI-Assisted Developer Shell  •  %s/%s\n",
		runtime.GOOS, runtime.GOARCH)
	fmt.Printf("  Team of 5 OS Project  •  Type 'help' to get started\n\n")

	os.Exit(shell.Run())
}
