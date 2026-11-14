package project_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nexshell/internal/project"
	"nexshell/internal/registry"
)

func TestProjectCmd_Detection(t *testing.T) {
	dir := t.TempDir()
	// Create sample go.mod
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module myapp\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := &project.ProjectCmd{}
	var out bytes.Buffer
	ctx := registry.Context{
		CWD:    &dir,
		Stdout: &out,
	}

	if err := cmd.Run(ctx, nil); err != nil {
		t.Fatalf("project command failed: %v", err)
	}

	res := out.String()
	if !strings.Contains(res, "Go (myapp)") {
		t.Fatalf("expected Go (myapp) in output: %s", res)
	}
	if !strings.Contains(res, "Go Tests") {
		t.Fatalf("expected Go Tests in output: %s", res)
	}
}
