// Package builtins defines all built-in shell commands for NexShell Phase 1.
// Each command satisfies registry.Command.
package builtins

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"nexshell/internal/registry"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func expandHome(path string) string {
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, path[1:])
		}
	}
	return path
}

// ─── cd ──────────────────────────────────────────────────────────────────────

type cdCmd struct{}

func (c *cdCmd) Name() string        { return "cd" }
func (c *cdCmd) Description() string { return "Change the current working directory" }
func (c *cdCmd) Run(ctx registry.Context, args []string) error {
	target := ""
	if len(args) == 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cd: cannot find home directory: %w", err)
		}
		target = home
	} else {
		target = expandHome(args[0])
	}
	if err := os.Chdir(target); err != nil {
		return fmt.Errorf("cd: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cd: %w", err)
	}
	*ctx.CWD = cwd
	return nil
}

// ─── pwd ─────────────────────────────────────────────────────────────────────

type pwdCmd struct{}

func (c *pwdCmd) Name() string        { return "pwd" }
func (c *pwdCmd) Description() string { return "Print the current working directory" }
func (c *pwdCmd) Run(ctx registry.Context, _ []string) error {
	fmt.Println(*ctx.CWD)
	return nil
}

// ─── ls ──────────────────────────────────────────────────────────────────────

type lsCmd struct{}

func (c *lsCmd) Name() string        { return "ls" }
func (c *lsCmd) Description() string { return "List directory contents" }
func (c *lsCmd) Run(ctx registry.Context, args []string) error {
	dir := *ctx.CWD
	if len(args) > 0 {
		dir = expandHome(args[0])
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("ls: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, e := range entries {
		info, _ := e.Info()
		mtime := ""
		size := ""
		if info != nil {
			mtime = info.ModTime().Format(time.DateTime)
			if !e.IsDir() {
				size = formatSize(info.Size())
			}
		}
		kind := " "
		name := e.Name()
		if e.IsDir() {
			kind = "d"
			name = colorize(name, colorBlue)
		} else if isExecutable(info) {
			name = colorize(name, colorGreen)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", kind, mtime, size, name)
	}
	return w.Flush()
}

func formatSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func isExecutable(fi os.FileInfo) bool {
	if fi == nil {
		return false
	}
	if runtime.GOOS == "windows" {
		name := strings.ToLower(fi.Name())
		return strings.HasSuffix(name, ".exe") ||
			strings.HasSuffix(name, ".bat") ||
			strings.HasSuffix(name, ".cmd")
	}
	return fi.Mode()&0o111 != 0
}

// ─── cat ─────────────────────────────────────────────────────────────────────

type catCmd struct{}

func (c *catCmd) Name() string        { return "cat" }
func (c *catCmd) Description() string { return "Print the contents of one or more files" }
func (c *catCmd) Run(_ registry.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("cat: missing file operand")
	}
	for _, path := range args {
		path = expandHome(path)
		f, err := os.Open(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cat: %v\n", err)
			continue
		}
		if _, err := io.Copy(os.Stdout, f); err != nil {
			f.Close()
			return fmt.Errorf("cat: %w", err)
		}
		f.Close()
	}
	return nil
}

// ─── echo ─────────────────────────────────────────────────────────────────────

type echoCmd struct{}

func (c *echoCmd) Name() string        { return "echo" }
func (c *echoCmd) Description() string { return "Print arguments to standard output" }
func (c *echoCmd) Run(_ registry.Context, args []string) error {
	fmt.Println(strings.Join(args, " "))
	return nil
}

// ─── clear ───────────────────────────────────────────────────────────────────

type clearCmd struct{}

func (c *clearCmd) Name() string        { return "clear" }
func (c *clearCmd) Description() string { return "Clear the terminal screen" }
func (c *clearCmd) Run(_ registry.Context, _ []string) error {
	if runtime.GOOS == "windows" {
		fmt.Print("\033[H\033[2J")
	} else {
		fmt.Print("\033[H\033[2J")
	}
	return nil
}

// ─── exit ────────────────────────────────────────────────────────────────────

type exitCmd struct{}

func (c *exitCmd) Name() string        { return "exit" }
func (c *exitCmd) Description() string { return "Exit the shell" }
func (c *exitCmd) Run(ctx registry.Context, args []string) error {
	code := 0
	if len(args) > 0 {
		n, err := strconv.Atoi(args[0])
		if err == nil {
			code = n
		}
	}
	// Save history before exiting.
	_ = ctx.History.Save()
	os.Exit(code)
	return nil
}

// ─── help ─────────────────────────────────────────────────────────────────────

type helpCmd struct{}

func (c *helpCmd) Name() string        { return "help" }
func (c *helpCmd) Description() string { return "Show help for all commands or a specific command" }
func (c *helpCmd) Run(ctx registry.Context, args []string) error {
	if len(args) > 0 {
		name := args[0]
		cmd, ok := ctx.Registry.Lookup(name)
		if !ok {
			return fmt.Errorf("help: unknown command %q", name)
		}
		fmt.Printf("%s\n  %s\n", colorize(cmd.Name(), colorCyan), cmd.Description())
		return nil
	}
	fmt.Println(colorize("NexShell — Available Commands", colorCyan))
	fmt.Println(strings.Repeat("─", 45))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, cmd := range ctx.Registry.All() {
		fmt.Fprintf(w, "  %s\t%s\n", colorize(cmd.Name(), colorGreen), cmd.Description())
	}
	return w.Flush()
}

// ─── env ──────────────────────────────────────────────────────────────────────

type envCmd struct{}

func (c *envCmd) Name() string        { return "env" }
func (c *envCmd) Description() string { return "Print environment variables (or a specific one)" }
func (c *envCmd) Run(_ registry.Context, args []string) error {
	if len(args) > 0 {
		key := args[0]
		val := os.Getenv(key)
		if val == "" {
			return fmt.Errorf("env: %s is not set", key)
		}
		fmt.Printf("%s=%s\n", key, val)
		return nil
	}
	vars := os.Environ()
	sort.Strings(vars)
	for _, v := range vars {
		fmt.Println(v)
	}
	return nil
}

// ─── history ──────────────────────────────────────────────────────────────────

type historyCmd struct{}

func (c *historyCmd) Name() string        { return "history" }
func (c *historyCmd) Description() string { return "Display command history" }
func (c *historyCmd) Run(ctx registry.Context, _ []string) error {
	entries := ctx.History.List()
	if len(entries) == 0 {
		fmt.Println("(no history)")
		return nil
	}
	for i, e := range entries {
		fmt.Printf("  %4d  %s\n", i+1, e)
	}
	return nil
}

// ─── alias ────────────────────────────────────────────────────────────────────

type aliasCmd struct{}

func (c *aliasCmd) Name() string        { return "alias" }
func (c *aliasCmd) Description() string { return "Create or list command aliases: alias name=value" }
func (c *aliasCmd) Run(ctx registry.Context, args []string) error {
	if len(args) == 0 {
		// List all aliases.
		all := ctx.Aliases.All()
		if len(all) == 0 {
			fmt.Println("(no aliases)")
			return nil
		}
		keys := make([]string, 0, len(all))
		for k := range all {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("alias %s='%s'\n", colorize(k, colorGreen), all[k])
		}
		return nil
	}
	// Set aliases from "name=value" pairs.
	for _, arg := range args {
		idx := strings.IndexByte(arg, '=')
		if idx < 1 {
			return fmt.Errorf("alias: invalid format %q — expected name=value", arg)
		}
		name := arg[:idx]
		value := arg[idx+1:]
		// Strip surrounding quotes from value.
		if len(value) >= 2 &&
			((value[0] == '\'' && value[len(value)-1] == '\'') ||
				(value[0] == '"' && value[len(value)-1] == '"')) {
			value = value[1 : len(value)-1]
		}
		ctx.Aliases.Set(name, value)
		fmt.Printf("alias %s='%s'\n", colorize(name, colorGreen), value)
	}
	return nil
}

// ─── unalias ──────────────────────────────────────────────────────────────────

type unaliasCmd struct{}

func (c *unaliasCmd) Name() string        { return "unalias" }
func (c *unaliasCmd) Description() string { return "Remove an alias" }
func (c *unaliasCmd) Run(ctx registry.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("unalias: missing argument")
	}
	for _, name := range args {
		ctx.Aliases.Delete(name)
	}
	return nil
}

// ─── ANSI colour helpers ──────────────────────────────────────────────────────

const (
	colorReset = "\033[0m"
	colorBlue  = "\033[34m"
	colorGreen = "\033[32m"
	colorCyan  = "\033[36m"
)

func colorize(s, code string) string {
	return code + s + colorReset
}

// ─── RegisterAll ──────────────────────────────────────────────────────────────

// RegisterAll registers every built-in command with reg.
func RegisterAll(reg *registry.Registry) {
	reg.Register(&cdCmd{})
	reg.Register(&pwdCmd{})
	reg.Register(&lsCmd{})
	reg.Register(&catCmd{})
	reg.Register(&echoCmd{})
	reg.Register(&clearCmd{})
	reg.Register(&exitCmd{})
	reg.Register(&helpCmd{})
	reg.Register(&envCmd{})
	reg.Register(&historyCmd{})
	reg.Register(&aliasCmd{})
	reg.Register(&unaliasCmd{})
}
