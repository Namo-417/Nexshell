package process_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"nexshell/internal/process"
	"nexshell/internal/registry"
)

func TestListProcesses(t *testing.T) {
	procs, err := process.ListProcesses()
	if err != nil {
		t.Fatalf("ListProcesses failed: %v", err)
	}
	if len(procs) == 0 {
		t.Fatal("expected at least one process")
	}

	foundSelf := false
	for _, p := range procs {
		if p.PID > 0 && p.Name != "" {
			foundSelf = true
			break
		}
	}
	if !foundSelf {
		t.Fatal("did not find any valid process with PID > 0 and a non-empty name")
	}
}

func TestProcessesCmd_Run(t *testing.T) {
	cmd := &process.ProcessesCmd{}
	var out bytes.Buffer
	ctx := registry.Context{
		Stdout: &out,
	}

	if err := cmd.Run(ctx, nil); err != nil {
		t.Fatalf("processes run failed: %v", err)
	}

	res := out.String()
	if !strings.Contains(res, "PID") || !strings.Contains(res, "NAME") {
		t.Fatalf("expected header in output, got:\n%s", res)
	}
}

func TestJobsManager(t *testing.T) {
	mgr := process.NewManager()
	waitCh := make(chan int, 1)
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	job := mgr.Add("test-cmd", 9999, cancel, waitCh)
	if job.ID != 1 || job.State != process.JobRunning {
		t.Fatalf("unexpected job: %#v", job)
	}

	jobs := mgr.List()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}

	// Test JobsCmd
	jobsCmd := process.NewJobsCmd(mgr)
	var out bytes.Buffer
	shellCtx := registry.Context{Stdout: &out}
	if err := jobsCmd.Run(shellCtx, nil); err != nil {
		t.Fatalf("jobs command failed: %v", err)
	}
	if !strings.Contains(out.String(), "test-cmd") {
		t.Fatalf("jobs output missing command: %s", out.String())
	}

	// Finish job
	waitCh <- 0
	time.Sleep(50 * time.Millisecond)

	updated, ok := mgr.Get(1)
	if !ok || updated.State != process.JobDone {
		t.Fatalf("expected job to be Done, got: %#v", updated)
	}
}
