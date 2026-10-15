// Package builtins defines NexShell's built-in shell commands.
// Each command satisfies registry.Command.
package builtins

import (
	"context"
	"errors"
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

func resolvePath(ctx registry.Context, path string) string {
	path = expandHome(path)
	if !filepath.IsAbs(path) && ctx.CWD != nil {
		return filepath.Join(*ctx.CWD, path)
	}
	return path
}

// A command may be canceled between reads; the executor also closes its owned
// pipe and file handles to wake blocked pipeline I/O.
type commandReader struct {
	ctx context.Context
	in  io.Reader
}

func (r commandReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.in.Read(p)
}

type commandWriter struct {
	ctx context.Context
	out io.Writer
}

func (w commandWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.out.Write(p)
}

func copyInput(ctx registry.Context, in io.Reader) error {
	_, err := io.Copy(commandWriter{ctx.Cancellation(), ctx.Output()}, commandReader{ctx.Cancellation(), in})
	return err
}

// ─── cd ──────────────────────────────────────────────────────────────────────

type cdCmd struct{}

func (c *cdCmd) Name() string        { return "cd" }
func (c *cdCmd) Description() string { return "Change the current working directory" }
func (c *cdCmd) PipelineSafe() bool  { return false }
func (c *cdCmd) Run(ctx registry.Context, args []string) error {
	if ctx.CWD == nil {
		return fmt.Errorf("cd: shell working directory is unavailable")
	}
	target := ""
	if len(args) == 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cd: cannot find home directory: %w", err)
		}
		target = home
	} else {
		target = resolvePath(ctx, args[0])
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
func (c *pwdCmd) PipelineSafe() bool  { return true }
func (c *pwdCmd) Run(ctx registry.Context, _ []string) error {
	if ctx.CWD == nil {
		return fmt.Errorf("pwd: shell working directory is unavailable")
	}
	_, err := fmt.Fprintln(ctx.Output(), *ctx.CWD)
	return err
}

// ─── ls ──────────────────────────────────────────────────────────────────────

type lsCmd struct{}

func (c *lsCmd) Name() string        { return "ls" }
func (c *lsCmd) Description() string { return "List directory contents" }
func (c *lsCmd) PipelineSafe() bool  { return true }
func (c *lsCmd) Run(ctx registry.Context, args []string) error {
	dir := "."
	if ctx.CWD != nil {
		dir = *ctx.CWD
	}
	if len(args) > 0 {
		dir = resolvePath(ctx, args[0])
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("ls: %w", err)
	}

	w := tabwriter.NewWriter(ctx.Output(), 0, 0, 2, ' ', 0)
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
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", kind, mtime, size, name); err != nil {
			return err
		}
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
func (c *catCmd) PipelineSafe() bool  { return true }
func (c *catCmd) Run(ctx registry.Context, args []string) error {
	if len(args) == 0 {
		// Preserve the old direct command API, while explicit engine streams
		// permit `cat < file` and `producer | cat`.
		if ctx.Stdin == nil {
			return fmt.Errorf("cat: missing file operand")
		}
		if err := copyInput(ctx, ctx.Input()); err != nil {
			return fmt.Errorf("cat: %w", err)
		}
		return nil
	}
	var failures []error
	for _, path := range args {
		if err := ctx.Cancellation().Err(); err != nil {
			return errors.Join(append(failures, fmt.Errorf("cat: %w", err))...)
		}
		path = resolvePath(ctx, path)
		info, err := os.Stat(path)
		if err != nil {
			failures = append(failures, fmt.Errorf("cat: %w", err))
			continue
		}
		if !info.Mode().IsRegular() {
			failures = append(failures, fmt.Errorf("cat: %q: only regular files are supported", path))
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			failures = append(failures, fmt.Errorf("cat: %w", err))
			continue
		}
		copyErr := copyInput(ctx, f)
		closeErr := f.Close()
		if copyErr != nil {
			return errors.Join(append(failures, fmt.Errorf("cat: %w", copyErr))...)
		}
		if closeErr != nil {
			failures = append(failures, fmt.Errorf("cat: %w", closeErr))
		}
	}
	return errors.Join(failures...)
}

// ─── echo ─────────────────────────────────────────────────────────────────────

type echoCmd struct{}

func (c *echoCmd) Name() string        { return "echo" }
func (c *echoCmd) Description() string { return "Print arguments to standard output" }
func (c *echoCmd) PipelineSafe() bool  { return true }
func (c *echoCmd) Run(ctx registry.Context, args []string) error {
	_, err := fmt.Fprintln(ctx.Output(), strings.Join(args, " "))
	return err
}

// ─── clear ───────────────────────────────────────────────────────────────────

type clearCmd struct{}

func (c *clearCmd) Name() string        { return "clear" }
func (c *clearCmd) Description() string { return "Clear the terminal screen" }
func (c *clearCmd) PipelineSafe() bool  { return true }
func (c *clearCmd) Run(ctx registry.Context, _ []string) error {
	_, err := fmt.Fprint(ctx.Output(), "\033[H\033[2J")
	return err
}

// ─── exit ────────────────────────────────────────────────────────────────────

type exitCmd struct{}

func (c *exitCmd) Name() string        { return "exit" }
func (c *exitCmd) Description() string { return "Exit the shell" }
func (c *exitCmd) PipelineSafe() bool  { return false }
func (c *exitCmd) Run(_ registry.Context, args []string) error {
	code := 0
	if len(args) > 1 {
		return fmt.Errorf("exit: expected at most one exit code")
	}
	if len(args) > 0 {
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 0 || n > 255 {
			return fmt.Errorf("exit: invalid exit code %q (expected 0-255)", args[0])
		}
		code = n
	}
	return &registry.ExitError{Code: code}
}

// ─── help ─────────────────────────────────────────────────────────────────────

type helpCmd struct{}

func (c *helpCmd) Name() string        { return "help" }
func (c *helpCmd) Description() string { return "Show help for all commands or a specific command" }
func (c *helpCmd) PipelineSafe() bool  { return true }
func (c *helpCmd) Run(ctx registry.Context, args []string) error {
	if len(args) > 0 {
		name := args[0]
		cmd, ok := ctx.Registry.Lookup(name)
		if !ok {
			return fmt.Errorf("help: unknown command %q", name)
		}
		_, err := fmt.Fprintf(ctx.Output(), "%s\n  %s\n", colorize(cmd.Name(), colorCyan), cmd.Description())
		return err
	}
	if _, err := fmt.Fprintln(ctx.Output(), colorize("NexShell — Available Commands", colorCyan)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(ctx.Output(), strings.Repeat("─", 45)); err != nil {
		return err
	}
	w := tabwriter.NewWriter(ctx.Output(), 0, 0, 2, ' ', 0)
	for _, cmd := range ctx.Registry.All() {
		if _, err := fmt.Fprintf(w, "  %s\t%s\n", colorize(cmd.Name(), colorGreen), cmd.Description()); err != nil {
			return err
		}
	}
	return w.Flush()
}

// ─── env ──────────────────────────────────────────────────────────────────────

type envCmd struct{}

func (c *envCmd) Name() string        { return "env" }
func (c *envCmd) Description() string { return "Print environment variables (or a specific one)" }
func (c *envCmd) PipelineSafe() bool  { return true }
func (c *envCmd) Run(ctx registry.Context, args []string) error {
	if len(args) > 0 {
		key := args[0]
		val, exists := os.LookupEnv(key)
		if !exists {
			return fmt.Errorf("env: %s is not set", key)
		}
		_, err := fmt.Fprintf(ctx.Output(), "%s=%s\n", key, val)
		return err
	}
	vars := os.Environ()
	sort.Strings(vars)
	for _, v := range vars {
		if _, err := fmt.Fprintln(ctx.Output(), v); err != nil {
			return err
		}
	}
	return nil
}

// ─── history ──────────────────────────────────────────────────────────────────

type historyCmd struct{}

func (c *historyCmd) Name() string        { return "history" }
func (c *historyCmd) Description() string { return "Display command history" }
func (c *historyCmd) PipelineSafe() bool  { return true }
func (c *historyCmd) Run(ctx registry.Context, _ []string) error {
	entries := ctx.History.List()
	if len(entries) == 0 {
		_, err := fmt.Fprintln(ctx.Output(), "(no history)")
		return err
	}
	for i, e := range entries {
		if _, err := fmt.Fprintf(ctx.Output(), "  %4d  %s\n", i+1, e); err != nil {
			return err
		}
	}
	return nil
}

// ─── alias ────────────────────────────────────────────────────────────────────

type aliasCmd struct{}

func (c *aliasCmd) Name() string        { return "alias" }
func (c *aliasCmd) Description() string { return "Create or list command aliases: alias name=value" }
func (c *aliasCmd) PipelineSafe() bool  { return false }
func (c *aliasCmd) Run(ctx registry.Context, args []string) error {
	if len(args) == 0 {
		// List all aliases.
		all := ctx.Aliases.All()
		if len(all) == 0 {
			_, err := fmt.Fprintln(ctx.Output(), "(no aliases)")
			return err
		}
		keys := make([]string, 0, len(all))
		for k := range all {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if _, err := fmt.Fprintf(ctx.Output(), "alias %s='%s'\n", colorize(k, colorGreen), all[k]); err != nil {
				return err
			}
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
		// The parser has already removed syntax quotes. Any quotes left in
		// the value intentionally belong to the command being aliased.
		ctx.Aliases.Set(name, value)
		if _, err := fmt.Fprintf(ctx.Output(), "alias %s='%s'\n", colorize(name, colorGreen), value); err != nil {
			return err
		}
	}
	return nil
}

// ─── unalias ──────────────────────────────────────────────────────────────────

type unaliasCmd struct{}

func (c *unaliasCmd) Name() string        { return "unalias" }
func (c *unaliasCmd) Description() string { return "Remove an alias" }
func (c *unaliasCmd) PipelineSafe() bool  { return false }
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
