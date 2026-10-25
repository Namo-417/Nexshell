// Package task loads nex.yaml and runs named tasks through the existing
// parser and executor. Task Engine v1 executes a task's commands sequentially
// and does not run depends, DAGs, plugins, or remote sources.
package task

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
func (c *Command) Description() string { return "Run a named task from nex.yaml" }
func (c *Command) PipelineSafe() bool  { return false }

func (c *Command) Run(ctx registry.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("task: missing task name")
	}
	if len(args) > 1 {
		return fmt.Errorf("task: expected a single task name")
	}
	return Run(ctx, args[0])
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

// Run loads nex.yaml from the shell working directory and executes the named
// task's commands in order, stopping at the first failure. Depends are loaded
// but not executed.
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
		// Nested Execute already printed the command diagnostic.
		return fmt.Errorf("task %q: command failed", name)
	}
	return nil
}
