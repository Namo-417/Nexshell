// Command nexshell is the entry point for the NexShell interactive shell.
// It wires up the command registry, alias store, history, registers all
// built-in commands, and starts the REPL.
package main

import (
	"fmt"
	"os"
	"runtime"

	"nexshell/internal/alias"
	"nexshell/internal/builtins"
	"nexshell/internal/history"
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
	// Print the welcome banner.
	fmt.Print("\033[36m" + banner + "\033[0m")
	fmt.Printf("  NexShell v0.2.0-phase2  •  %s/%s  •  type 'help' to get started\n\n",
		runtime.GOOS, runtime.GOARCH)

	// Build registry.
	reg := registry.New()

	// Build alias and history stores.
	aliases := alias.New()
	hist := history.New()

	// Register all built-in commands.
	builtins.RegisterAll(reg)

	// Create and run the REPL.
	shell, err := repl.New(reg, aliases, hist)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexshell: fatal: %v\n", err)
		os.Exit(1)
	}
	os.Exit(shell.Run())
}
