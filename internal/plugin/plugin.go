// Package plugin provides plugin discovery, loading, and execution for NexShell.
package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"nexshell/internal/registry"
)

// Plugin represents an external integration or built-in plugin.
type Plugin struct {
	Name        string   `json:"name" yaml:"name"`
	Version     string   `json:"version" yaml:"version"`
	Description string   `json:"description" yaml:"description"`
	Path        string   `json:"path" yaml:"path"`
	Commands    []string `json:"commands" yaml:"commands"`
	IsBuiltin   bool     `json:"is_builtin" yaml:"is_builtin"`
}

// Manager scans, discovers, and executes plugins.
type Manager struct {
	pluginDirs []string
	plugins    map[string]*Plugin
}

var defaultManager *Manager

func DefaultManager() *Manager {
	if defaultManager == nil {
		defaultManager = NewManager([]string{"./plugins", expandHome("~/.nexshell/plugins")})
	}
	return defaultManager
}

func NewManager(dirs []string) *Manager {
	m := &Manager{
		pluginDirs: dirs,
		plugins:    make(map[string]*Plugin),
	}
	// Register default core integrations (Git and Docker)
	m.registerBuiltins()
	_ = m.Discover()
	return m
}

func (m *Manager) registerBuiltins() {
	m.plugins["git"] = &Plugin{
		Name:        "git",
		Version:     "1.0.0",
		Description: "Git VCS integration: branch, commit and status inspection",
		Commands:    []string{"git-status", "git-branch", "git-log"},
		IsBuiltin:   true,
	}
	m.plugins["docker"] = &Plugin{
		Name:        "docker",
		Version:     "1.0.0",
		Description: "Docker container and image inspection",
		Commands:    []string{"docker-ps", "docker-images"},
		IsBuiltin:   true,
	}
}

// Discover scans directories for external plugins.
func (m *Manager) Discover() error {
	for _, dir := range m.pluginDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			pluginPath := filepath.Join(dir, e.Name())
			if e.IsDir() {
				// Check for plugin.json
				manifestPath := filepath.Join(pluginPath, "plugin.json")
				if data, err := os.ReadFile(manifestPath); err == nil {
					var p Plugin
					if err := json.Unmarshal(data, &p); err == nil {
						p.Path = pluginPath
						m.plugins[p.Name] = &p
					}
				}
			} else if strings.HasPrefix(e.Name(), "nex-") {
				name := strings.TrimPrefix(e.Name(), "nex-")
				name = strings.TrimSuffix(name, filepath.Ext(name))
				m.plugins[name] = &Plugin{
					Name:        name,
					Version:     "1.0.0",
					Description: fmt.Sprintf("Executable plugin %s", e.Name()),
					Path:        pluginPath,
					Commands:    []string{name},
				}
			}
		}
	}
	return nil
}

// List returns all registered plugins sorted by name.
func (m *Manager) List() []*Plugin {
	var list []*Plugin
	for _, p := range m.plugins {
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})
	return list
}

// Get finds a plugin by name.
func (m *Manager) Get(name string) (*Plugin, bool) {
	p, ok := m.plugins[name]
	return p, ok
}

// Execute runs a plugin or its builtin sub-command.
func (m *Manager) Execute(ctx registry.Context, name string, args []string) error {
	p, ok := m.Get(name)
	if !ok {
		return fmt.Errorf("plugin %q not found (run 'plugins list' to see available plugins)", name)
	}

	if p.IsBuiltin {
		return executeBuiltinPlugin(ctx, name, args)
	}

	cmd := exec.CommandContext(ctx.Cancellation(), p.Path, args...)
	if ctx.CWD != nil {
		cmd.Dir = *ctx.CWD
	}
	cmd.Stdin = ctx.Input()
	cmd.Stdout = ctx.Output()
	cmd.Stderr = ctx.ErrorOutput()
	return cmd.Run()
}

func executeBuiltinPlugin(ctx registry.Context, name string, args []string) error {
	switch name {
	case "git":
		gitArgs := []string{"status", "--short"}
		if len(args) > 0 {
			gitArgs = args
		}
		cmd := exec.CommandContext(ctx.Cancellation(), "git", gitArgs...)
		if ctx.CWD != nil {
			cmd.Dir = *ctx.CWD
		}
		cmd.Stdout = ctx.Output()
		cmd.Stderr = ctx.ErrorOutput()
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("git integration: %w", err)
		}
		return nil
	case "docker":
		dockerArgs := []string{"ps"}
		if len(args) > 0 {
			dockerArgs = args
		}
		cmd := exec.CommandContext(ctx.Cancellation(), "docker", dockerArgs...)
		if ctx.CWD != nil {
			cmd.Dir = *ctx.CWD
		}
		cmd.Stdout = ctx.Output()
		cmd.Stderr = ctx.ErrorOutput()
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("docker integration: %w (ensure Docker daemon is running)", err)
		}
		return nil
	default:
		return fmt.Errorf("unknown built-in plugin %q", name)
	}
}

// ─── Plugins Command ──────────────────────────────────────────────────────────

// PluginsCmd manages and runs plugins.
type PluginsCmd struct {
	mgr *Manager
}

func NewPluginsCmd(mgr *Manager) *PluginsCmd {
	if mgr == nil {
		mgr = DefaultManager()
	}
	return &PluginsCmd{mgr: mgr}
}

func (c *PluginsCmd) Name() string        { return "plugins" }
func (c *PluginsCmd) Description() string { return "Discover, inspect, and run plugins: plugins [list|info|run]" }
func (c *PluginsCmd) PipelineSafe() bool  { return true }

func (c *PluginsCmd) Run(ctx registry.Context, args []string) error {
	action := "list"
	if len(args) > 0 {
		action = args[0]
	}

	switch action {
	case "list":
		_ = c.mgr.Discover()
		plugins := c.mgr.List()
		tw := tabwriter.NewWriter(ctx.Output(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(ctx.Output(), "NexShell Plugins:")
		fmt.Fprintln(tw, "  NAME\tVERSION\tTYPE\tDESCRIPTION")
		fmt.Fprintln(tw, "  ────\t───────\t────\t───────────")
		for _, p := range plugins {
			pType := "external"
			if p.IsBuiltin {
				pType = "builtin"
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", p.Name, p.Version, pType, p.Description)
		}
		return tw.Flush()

	case "info":
		if len(args) < 2 {
			return fmt.Errorf("plugins info: missing plugin name")
		}
		p, ok := c.mgr.Get(args[1])
		if !ok {
			return fmt.Errorf("plugin %q not found", args[1])
		}
		fmt.Fprintf(ctx.Output(), "Plugin: %s\n", p.Name)
		fmt.Fprintf(ctx.Output(), "  Version:     %s\n", p.Version)
		fmt.Fprintf(ctx.Output(), "  Description: %s\n", p.Description)
		fmt.Fprintf(ctx.Output(), "  Commands:    %s\n", strings.Join(p.Commands, ", "))
		fmt.Fprintf(ctx.Output(), "  Path:        %s\n", p.Path)
		return nil

	case "run":
		if len(args) < 2 {
			return fmt.Errorf("plugins run: missing plugin name")
		}
		return c.mgr.Execute(ctx, args[1], args[2:])

	default:
		// Default to running the plugin if name matches
		if _, ok := c.mgr.Get(action); ok {
			return c.mgr.Execute(ctx, action, args[1:])
		}
		return fmt.Errorf("plugins: unknown action %q (use list, info, or run)", action)
	}
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, path[1:])
		}
	}
	return path
}
