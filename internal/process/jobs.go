// Package process manages OS process inspection, background jobs,
// and process control for NexShell.
package process

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"nexshell/internal/registry"
)

// JobState represents the lifecycle status of a background job.
type JobState string

const (
	JobRunning    JobState = "Running"
	JobStopped    JobState = "Stopped"
	JobDone       JobState = "Done"
	JobTerminated JobState = "Terminated"
)

// Job represents a tracked background execution task.
type Job struct {
	ID        int
	PID       int
	Command   string
	State     JobState
	StartTime time.Time
	Cancel    context.CancelFunc
	WaitCh    chan int
	ExitCode  int
}

// Manager coordinates background jobs in the shell.
type Manager struct {
	mu     sync.Mutex
	nextID int
	jobs   map[int]*Job
}

var (
	defaultManager = NewManager()
)

// DefaultManager returns the global job manager singleton.
func DefaultManager() *Manager {
	return defaultManager
}

// NewManager creates a new job manager.
func NewManager() *Manager {
	return &Manager{
		nextID: 1,
		jobs:   make(map[int]*Job),
	}
}

// Add tracks a new background job.
func (m *Manager) Add(command string, pid int, cancel context.CancelFunc, waitCh chan int) *Job {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := m.nextID
	m.nextID++

	job := &Job{
		ID:        id,
		PID:       pid,
		Command:   command,
		State:     JobRunning,
		StartTime: time.Now(),
		Cancel:    cancel,
		WaitCh:    waitCh,
	}
	m.jobs[id] = job

	// Monitor completion in background
	if waitCh != nil {
		go func() {
			exitCode := <-waitCh
			m.mu.Lock()
			job.State = JobDone
			job.ExitCode = exitCode
			m.mu.Unlock()
		}()
	}

	return job
}

// List returns a snapshot of all tracked jobs.
func (m *Manager) List() []*Job {
	m.mu.Lock()
	defer m.mu.Unlock()

	var list []*Job
	for _, j := range m.jobs {
		list = append(list, j)
	}
	return list
}

// Get finds a job by its numeric ID.
func (m *Manager) Get(id int) (*Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	j, ok := m.jobs[id]
	return j, ok
}

// GetByPID finds a job by process ID.
func (m *Manager) GetByPID(pid int) (*Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, j := range m.jobs {
		if j.PID == pid {
			return j, true
		}
	}
	return nil, false
}

// Remove cleans up a finished job.
func (m *Manager) Remove(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.jobs, id)
}

// ─── JOBS Command ─────────────────────────────────────────────────────────────

// JobsCmd lists active and recent background jobs.
type JobsCmd struct {
	mgr *Manager
}

func NewJobsCmd(mgr *Manager) *JobsCmd {
	if mgr == nil {
		mgr = DefaultManager()
	}
	return &JobsCmd{mgr: mgr}
}

func (c *JobsCmd) Name() string        { return "jobs" }
func (c *JobsCmd) Description() string { return "List background jobs and their statuses" }
func (c *JobsCmd) PipelineSafe() bool  { return true }

func (c *JobsCmd) Run(ctx registry.Context, _ []string) error {
	jobs := c.mgr.List()
	if len(jobs) == 0 {
		_, err := fmt.Fprintln(ctx.Output(), "(no active jobs)")
		return err
	}

	tw := tabwriter.NewWriter(ctx.Output(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "JOB\tPID\tSTATUS\tCOMMAND")
	fmt.Fprintln(tw, "───\t───\t──────\t───────")
	for _, j := range jobs {
		fmt.Fprintf(tw, "[%d]\t%d\t%s\t%s\n", j.ID, j.PID, j.State, j.Command)
	}
	return tw.Flush()
}

// ─── FG Command ───────────────────────────────────────────────────────────────

// FgCmd brings a background job to foreground.
type FgCmd struct {
	mgr *Manager
}

func NewFgCmd(mgr *Manager) *FgCmd {
	if mgr == nil {
		mgr = DefaultManager()
	}
	return &FgCmd{mgr: mgr}
}

func (c *FgCmd) Name() string        { return "fg" }
func (c *FgCmd) Description() string { return "Bring a background job to the foreground: fg [%job_id]" }
func (c *FgCmd) PipelineSafe() bool  { return false }

func (c *FgCmd) Run(ctx registry.Context, args []string) error {
	id := 1
	if len(args) > 0 {
		s := strings.TrimPrefix(args[0], "%")
		var err error
		id, err = strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("fg: invalid job specification %q", args[0])
		}
	}

	job, ok := c.mgr.Get(id)
	if !ok {
		return fmt.Errorf("fg: job %d not found", id)
	}

	if job.State == JobDone || job.State == JobTerminated {
		c.mgr.Remove(id)
		fmt.Fprintf(ctx.Output(), "[%d] %s (already finished)\n", job.ID, job.Command)
		return nil
	}

	fmt.Fprintf(ctx.Output(), "%s (PID %d)\n", job.Command, job.PID)
	if job.WaitCh != nil {
		select {
		case code := <-job.WaitCh:
			job.State = JobDone
			job.ExitCode = code
			c.mgr.Remove(id)
			if code != 0 {
				return fmt.Errorf("job exited with code %d", code)
			}
			return nil
		case <-ctx.Cancellation().Done():
			if job.Cancel != nil {
				job.Cancel()
			}
			return ctx.Cancellation().Err()
		}
	}
	return nil
}

// ─── BG Command ───────────────────────────────────────────────────────────────

// BgCmd resumes a stopped job in background.
type BgCmd struct {
	mgr *Manager
}

func NewBgCmd(mgr *Manager) *BgCmd {
	if mgr == nil {
		mgr = DefaultManager()
	}
	return &BgCmd{mgr: mgr}
}

func (c *BgCmd) Name() string        { return "bg" }
func (c *BgCmd) Description() string { return "Resume a suspended background job: bg [%job_id]" }
func (c *BgCmd) PipelineSafe() bool  { return false }

func (c *BgCmd) Run(ctx registry.Context, args []string) error {
	id := 1
	if len(args) > 0 {
		s := strings.TrimPrefix(args[0], "%")
		var err error
		id, err = strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("bg: invalid job specification %q", args[0])
		}
	}

	job, ok := c.mgr.Get(id)
	if !ok {
		return fmt.Errorf("bg: job %d not found", id)
	}

	if job.State == JobRunning {
		fmt.Fprintf(ctx.Output(), "job [%d] %s is already running\n", job.ID, job.Command)
		return nil
	}

	job.State = JobRunning
	fmt.Fprintf(ctx.Output(), "[%d] %s resumed\n", job.ID, job.Command)
	return nil
}

// ─── KILL Command ─────────────────────────────────────────────────────────────

// KillCmd terminates a process or background job.
type KillCmd struct {
	mgr *Manager
}

func NewKillCmd(mgr *Manager) *KillCmd {
	if mgr == nil {
		mgr = DefaultManager()
	}
	return &KillCmd{mgr: mgr}
}

func (c *KillCmd) Name() string        { return "kill" }
func (c *KillCmd) Description() string { return "Terminate a process or job: kill [-9] <pid | %job_id>" }
func (c *KillCmd) PipelineSafe() bool  { return false }

func (c *KillCmd) Run(ctx registry.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("kill: missing pid or job operand")
	}

	target := args[0]
	if target == "-9" || target == "-SIGKILL" {
		if len(args) < 2 {
			return fmt.Errorf("kill: missing pid or job operand")
		}
		target = args[1]
	}

	// Check if targeting job (%id)
	if strings.HasPrefix(target, "%") {
		id, err := strconv.Atoi(strings.TrimPrefix(target, "%"))
		if err != nil {
			return fmt.Errorf("kill: invalid job id %q", target)
		}
		job, ok := c.mgr.Get(id)
		if !ok {
			return fmt.Errorf("kill: job %d not found", id)
		}
		if job.Cancel != nil {
			job.Cancel()
		}
		if job.PID > 0 {
			if p, err := os.FindProcess(job.PID); err == nil {
				_ = p.Kill()
			}
		}
		job.State = JobTerminated
		fmt.Fprintf(ctx.Output(), "[%d] terminated\n", job.ID)
		return nil
	}

	// Target PID
	pid, err := strconv.Atoi(target)
	if err != nil {
		return fmt.Errorf("kill: invalid PID %q", target)
	}

	// Check if it's one of our tracked jobs
	if job, ok := c.mgr.GetByPID(pid); ok {
		if job.Cancel != nil {
			job.Cancel()
		}
		job.State = JobTerminated
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("kill: %w", err)
	}
	if err := proc.Kill(); err != nil {
		return fmt.Errorf("kill PID %d: %w", pid, err)
	}

	fmt.Fprintf(ctx.Output(), "Process %d terminated\n", pid)
	return nil
}
