package plugin_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nexshell/internal/plugin"
	"nexshell/internal/registry"
)

func TestPlugin_DiscoveryAndList(t *testing.T) {
	tempDir := t.TempDir()

	// Create a dummy plugin directory with plugin.json
	customPluginDir := filepath.Join(tempDir, "custom")
	if err := os.MkdirAll(customPluginDir, 0o755); err != nil {
		t.Fatal(err)
	}

	manifest := plugin.Plugin{
		Name:        "custom",
		Version:     "2.1.0",
		Description: "A test custom plugin",
		Commands:    []string{"custom-run"},
	}
	manifestBytes, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(customPluginDir, "plugin.json"), manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	mgr := plugin.NewManager([]string{tempDir})
	p, ok := mgr.Get("custom")
	if !ok {
		t.Fatal("expected custom plugin to be discovered")
	}
	if p.Version != "2.1.0" {
		t.Fatalf("expected version 2.1.0, got %s", p.Version)
	}

	// Verify built-ins exist
	if _, ok := mgr.Get("git"); !ok {
		t.Fatal("expected git builtin plugin to be present")
	}
	if _, ok := mgr.Get("docker"); !ok {
		t.Fatal("expected docker builtin plugin to be present")
	}

	// Test PluginsCmd
	cmd := plugin.NewPluginsCmd(mgr)
	var out bytes.Buffer
	ctx := registry.Context{Stdout: &out}

	if err := cmd.Run(ctx, []string{"list"}); err != nil {
		t.Fatalf("plugins list failed: %v", err)
	}

	res := out.String()
	if !strings.Contains(res, "custom") || !strings.Contains(res, "git") {
		t.Fatalf("unexpected plugin list output: %s", res)
	}
}

func TestPlugin_Info(t *testing.T) {
	mgr := plugin.NewManager(nil)
	cmd := plugin.NewPluginsCmd(mgr)
	var out bytes.Buffer
	ctx := registry.Context{Stdout: &out}

	if err := cmd.Run(ctx, []string{"info", "git"}); err != nil {
		t.Fatalf("plugins info git failed: %v", err)
	}

	res := out.String()
	if !strings.Contains(res, "Git VCS integration") {
		t.Fatalf("unexpected info output: %s", res)
	}
}
