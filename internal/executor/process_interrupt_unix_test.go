//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || aix

package executor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"nexshell/internal/parser"
)

func TestUnixChildInterruptCleansPipeline(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "sibling.pid")
	status, err, _, diagnostics := runPlan(t, parser.Plan{Commands: []parser.Command{
		fixture(t, "wait", pidFile), fixture(t, "interrupt"),
	}}, dir, nil)
	if status != 130 || !errors.Is(err, context.Canceled) {
		t.Fatalf("status=%d err=%v stderr=%q", status, err, diagnostics)
	}
	// Startup and signal delivery may precede the fixture writing its PID;
	// the executor must reap the started sibling in either ordering.
	if content, err := os.ReadFile(pidFile); err == nil {
		pid, err := strconv.Atoi(string(content))
		if err != nil {
			t.Fatal(err)
		}
		assertProcessStopped(t, pid)
	}
}
