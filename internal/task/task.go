// Package task loads nex.yaml and runs named tasks through the existing
// parser and executor. Task Engine v2 supports DAG dependency resolution,
// cycle detection, and parallel task execution (--parallel).
package task

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"

	"gopkg.in/yaml.v3"

	"nexshell/internal/parser"
	"nexshell/internal/registry"
)

// ExecuteFunc runs one parsed plan. Production wiring supplies executor.Execute
// so this package does not import the executor (which would cycle through tests).
type ExecuteFunc func(ctx context.Context, plan parser.Plan, shell registry.Context) (int, error)

var executePlan ExecuteFunc

// SetExecute installs the plan runner used by task commands. The REPL sets this
// to executor.Execute; tests do the same.
func SetExecute(fn ExecuteFunc) {
	executePlan = fn
}

const fileName = "nex.yaml"

// Config is the nex.yaml document.
type Config struct {
	Version string          `yaml:"version"`
	Project string          `yaml:"project"`
	Tasks   map[string]Task `yaml:"tasks"`
}

// Task is one named entry under tasks.
type Task struct {
	Description string   `yaml:"description"`
	Commands    []string `yaml:"commands"`
	Depends     []string `yaml:"depends"`
}

// Command is the pipeline-unsafe `task` built-in.
type Command struct{}

func (c *Command) Name() string        { return "task" }
func (c *Command) Description() string { return "Run tasks defined in nex.yaml: task <name> [--parallel]" }
func (c *Command) PipelineSafe() bool  { return false }

func (c *Command) Run(ctx registry.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("task: missing task name")
	}

	taskName := ""
	parallel := false
	withDeps := false

	for _, a := range args {
		switch a {
		case "--parallel", "-p":
			parallel = true
			withDeps = true
		case "--with-deps", "--deps", "-d":
			withDeps = true
		case "--list", "-l", "list":
			return ListTasks(ctx)
		default:
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("task: unknown flag %q", a)
			}
			if taskName == "" {
				taskName = a
			} else {
				return fmt.Errorf("task: expected a single task name")
			}
		}
	}

	if taskName == "" {
		return fmt.Errorf("task: missing task name")
	}

	if withDeps || parallel {
		return RunDAG(ctx, taskName, parallel)
	}

	return Run(ctx, taskName)
}

// ListTasks prints all tasks available in nex.yaml.
func ListTasks(ctx registry.Context) error {
	if ctx.CWD == nil {
		return fmt.Errorf("task: shell working directory is unavailable")
	}
	cfg, err := Load(filepath.Join(*ctx.CWD, fileName))
	if err != nil {
		return err
	}

	tw := tabwriter.NewWriter(ctx.Output(), 0, 0, 2, ' ', 0)
	fmt.Fprintf(ctx.Output(), "Tasks defined in %s (Project: %s):\n", fileName, cfg.Project)
	fmt.Fprintln(tw, "  TASK\tDESCRIPTION\tDEPENDS")
	fmt.Fprintln(tw, "  ────\t───────────\t───────")

	names := make([]string, 0, len(cfg.Tasks))
	for n := range cfg.Tasks {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, n := range names {
		t := cfg.Tasks[n]
		deps := "-"
		if len(t.Depends) > 0 {
			deps = strings.Join(t.Depends, ", ")
		}
		desc := t.Description
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", n, desc, deps)
	}
	return tw.Flush()
}

// Load reads and parses a nex.yaml file.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("task: nex.yaml not found")
		}
		return Config{}, fmt.Errorf("task: read nex.yaml: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("task: invalid nex.yaml: %w", err)
	}
	if cfg.Tasks == nil {
		cfg.Tasks = map[string]Task{}
	}
	return cfg, nil
}

// Lookup returns the named task.
func (c Config) Lookup(name string) (Task, error) {
	task, ok := c.Tasks[name]
	if !ok {
		return Task{}, fmt.Errorf("task: unknown task %q", name)
	}
	return task, nil
}

// Run executes the named task's commands in order, stopping at the first failure.
// Depends are not executed in Run to maintain backward compatibility with Phase 2.
func Run(ctx registry.Context, name string) error {
	if ctx.CWD == nil {
		return fmt.Errorf("task: shell working directory is unavailable")
	}
	if err := ctx.Cancellation().Err(); err != nil {
		return fmt.Errorf("task %q: canceled", name)
	}
	cfg, err := Load(filepath.Join(*ctx.CWD, fileName))
	if err != nil {
		return err
	}
	spec, err := cfg.Lookup(name)
	if err != nil {
		return err
	}
	return runTaskCommands(ctx, name, spec)
}

func runTaskCommands(ctx registry.Context, name string, spec Task) error {
	if len(spec.Commands) == 0 {
		return fmt.Errorf("task %q: no commands", name)
	}
	if executePlan == nil {
		return fmt.Errorf("task: executor is not configured")
	}
	for i, line := range spec.Commands {
		if err := ctx.Cancellation().Err(); err != nil {
			return fmt.Errorf("task %q: canceled", name)
		}
		if strings.TrimSpace(line) == "" {
			return fmt.Errorf("task %q: empty command", name)
		}
		plan, err := parser.Parse(line)
		if err != nil {
			return fmt.Errorf("task %q: parse command %d: %w", name, i+1, err)
		}
		_, execErr := executePlan(ctx.Cancellation(), plan, ctx)
		if execErr == nil {
			continue
		}
		var exit *registry.ExitError
		if errors.As(execErr, &exit) {
			return fmt.Errorf("task %q: nested exit is not allowed", name)
		}
		if errors.Is(execErr, context.Canceled) || errors.Is(execErr, context.DeadlineExceeded) {
			return fmt.Errorf("task %q: canceled", name)
		}
		return fmt.Errorf("task %q: command failed", name)
	}
	return nil
}

// RunDAG resolves task dependencies as a DAG, detects circular dependencies,
// and executes them in topological order (concurrently if parallel is true).
func RunDAG(ctx registry.Context, rootName string, parallel bool) error {
	if ctx.CWD == nil {
		return fmt.Errorf("task: shell working directory is unavailable")
	}
	if err := ctx.Cancellation().Err(); err != nil {
		return fmt.Errorf("task %q: canceled", rootName)
	}

	cfg, err := Load(filepath.Join(*ctx.CWD, fileName))
	if err != nil {
		return err
	}

	if _, err := cfg.Lookup(rootName); err != nil {
		return err
	}

	// 1. Resolve DAG and check for circular dependencies
	visiting := make(map[string]bool)
	visited := make(map[string]bool)
	var order []string

	var dfs func(curr string) error
	dfs = func(curr string) error {
		if visiting[curr] {
			return fmt.Errorf("task %q: circular dependency detected on %q", rootName, curr)
		}
		if visited[curr] {
			return nil
		}
		t, err := cfg.Lookup(curr)
		if err != nil {
			return fmt.Errorf("task %q: dependency error: %w", curr, err)
		}

		visiting[curr] = true
		for _, dep := range t.Depends {
			if err := dfs(dep); err != nil {
				return err
			}
		}
		visiting[curr] = false
		visited[curr] = true
		order = append(order, curr)
		return nil
	}

	if err := dfs(rootName); err != nil {
		return err
	}

	// 2. Execution
	if !parallel {
		// Sequential execution in topological order
		for _, taskName := range order {
			if err := ctx.Cancellation().Err(); err != nil {
				return fmt.Errorf("task %q: canceled", taskName)
			}
			spec, _ := cfg.Lookup(taskName)
			if taskName != rootName {
				fmt.Fprintf(ctx.Output(), "[task] Running dependency: %s\n", taskName)
			}
			if err := runTaskCommands(ctx, taskName, spec); err != nil {
				return err
			}
		}
		return nil
	}

	// Parallel execution: execute dependencies concurrently where possible
	// Dependencies can run in parallel; root runs after dependencies finish.
	deps := make([]string, 0, len(order)-1)
	for _, n := range order {
		if n != rootName {
			deps = append(deps, n)
		}
	}

	if len(deps) > 0 {
		var wg sync.WaitGroup
		errCh := make(chan error, len(deps))

		for _, dName := range deps {
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				spec, _ := cfg.Lookup(name)
				fmt.Fprintf(ctx.Output(), "[task:parallel] Started %s\n", name)
				if err := runTaskCommands(ctx, name, spec); err != nil {
					errCh <- fmt.Errorf("parallel task %q failed: %w", name, err)
				}
			}(dName)
		}

		wg.Wait()
		close(errCh)
		if firstErr := <-errCh; firstErr != nil {
			return firstErr
		}
	}

	// Execute root task commands (or in parallel if task has multiple commands)
	rootDirSpec, _ := cfg.Lookup(rootName)
	if len(rootDirSpec.Commands) > 1 && parallel {
		var wg sync.WaitGroup
		errCh := make(chan error, len(rootDirSpec.Commands))

		for i, line := range rootDirSpec.Commands {
			wg.Add(1)
			go func(cmdIdx int, cmdLine string) {
				defer wg.Done()
				plan, err := parser.Parse(cmdLine)
				if err != nil {
					errCh <- fmt.Errorf("task %q: parse command %d: %w", rootName, cmdIdx+1, err)
					return
				}
				_, execErr := executePlan(ctx.Cancellation(), plan, ctx)
				if execErr != nil {
					errCh <- fmt.Errorf("task %q: command failed", rootName)
				}
			}(i, line)
		}
		wg.Wait()
		close(errCh)
		if firstErr := <-errCh; firstErr != nil {
			return firstErr
		}
		return nil
	}

	return runTaskCommands(ctx, rootName, rootDirSpec)
}
