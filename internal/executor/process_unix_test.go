//go:build dragonfly || freebsd || linux || netbsd || openbsd || solaris || aix

package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"nexshell/internal/parser"
	"nexshell/internal/registry"
)

func assertProcessStopped(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return
		}
		// Linux can leave an orphan zombie until PID 1 reaps it. A zombie
		// has terminated and owns no descriptors, so it is also stopped.
		if stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
			if end := strings.LastIndex(string(stat), ") "); end >= 0 && strings.HasPrefix(string(stat[end+2:]), "Z ") {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("process %d survived cleanup", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestChildSIGINTCancelsCooperativeBuiltin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	completed := make(chan struct{})
	reg := registry.New()
	reg.Register(awaitCancellationCommand{completed})
	status, err := Execute(ctx, parser.Plan{Commands: []parser.Command{
		{Name: "await-cancellation"}, fixture(t, "interrupt"),
	}}, registry.Context{Registry: reg, Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard})
	if status != 130 || !errors.Is(err, context.Canceled) {
		t.Fatalf("status=%d err=%v", status, err)
	}
	if ctx.Err() != nil {
		t.Fatalf("child interrupt did not cancel promptly: %v", ctx.Err())
	}
	select {
	case <-completed:
	default:
		t.Fatal("builtin was not waited on")
	}
}
