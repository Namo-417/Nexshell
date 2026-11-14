// Package system provides system metrics, observability, execution audit logs,
// and performance benchmarks for NexShell.
package system

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"text/tabwriter"
	"time"

	"nexshell/internal/registry"
)

var startTime = time.Now()

// SystemCmd displays comprehensive operating system and runtime metrics.
type SystemCmd struct{}

func (c *SystemCmd) Name() string        { return "system" }
func (c *SystemCmd) Description() string { return "Show system information, CPU, memory and runtime metrics" }
func (c *SystemCmd) PipelineSafe() bool  { return true }

func (c *SystemCmd) Run(ctx registry.Context, _ []string) error {
	tw := tabwriter.NewWriter(ctx.Output(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(ctx.Output(), "\033[36m─── NexShell System Observability ────────────────────────────\033[0m")

	hostname, _ := os.Hostname()
	uptime := time.Since(startTime).Round(time.Second)

	fmt.Fprintf(tw, "  Hostname:\t%s\n", hostname)
	fmt.Fprintf(tw, "  OS / Platform:\t%s / %s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(tw, "  Logical CPUs:\t%d\n", runtime.NumCPU())
	fmt.Fprintf(tw, "  Shell Uptime:\t%s\n", uptime)
	fmt.Fprintf(tw, "  Shell PID:\t%d\n", os.Getpid())

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	allocMB := float64(memStats.Alloc) / (1024 * 1024)
	sysMB := float64(memStats.Sys) / (1024 * 1024)

	fmt.Fprintf(tw, "  Go Runtime:\t%s\n", runtime.Version())
	fmt.Fprintf(tw, "  Goroutines:\t%d\n", runtime.NumGoroutine())
	fmt.Fprintf(tw, "  Allocated Memory:\t%.2f MB\n", allocMB)
	fmt.Fprintf(tw, "  System Memory:\t%.2f MB\n", sysMB)
	fmt.Fprintf(tw, "  GC Cycles:\t%d\n", memStats.NumGC)

	_ = tw.Flush()
	fmt.Fprintln(ctx.Output(), "\033[36m──────────────────────────────────────────────────────────────\033[0m")
	return nil
}

// AuditRecord stores execution metadata for shell telemetry and auditing.
type AuditRecord struct {
	Timestamp time.Time     `json:"timestamp"`
	User      string        `json:"user"`
	CWD       string        `json:"cwd"`
	Command   string        `json:"command"`
	Duration  time.Duration `json:"duration"`
	ExitCode  int           `json:"exit_code"`
	RiskLevel string        `json:"risk_level"`
}

// AuditLogger manages persistent audit logging.
type AuditLogger struct {
	mu      sync.RWMutex
	records []AuditRecord
}

var (
	defaultLogger = &AuditLogger{}
	auditFileMu   sync.Mutex
)

func DefaultAuditLogger() *AuditLogger {
	return defaultLogger
}

func (l *AuditLogger) Log(rec AuditRecord) {
	l.mu.Lock()
	l.records = append(l.records, rec)
	l.mu.Unlock()

	// Write to .nexshell_audit.log file in background safely
	go func() {
		entry := fmt.Sprintf("[%s] exit=%d dur=%s user=%s risk=%s cmd=%q\n",
			rec.Timestamp.Format(time.RFC3339), rec.ExitCode, rec.Duration, rec.User, rec.RiskLevel, rec.Command)
		_ = appendAuditFile(entry)
	}()
}

func appendAuditFile(line string) error {
	auditFileMu.Lock()
	defer auditFileMu.Unlock()
	f, err := os.OpenFile(".nexshell_audit.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line)
	return err
}

func (l *AuditLogger) Recent(n int) []AuditRecord {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if n <= 0 || n > len(l.records) {
		out := make([]AuditRecord, len(l.records))
		copy(out, l.records)
		return out
	}
	start := len(l.records) - n
	out := make([]AuditRecord, n)
	copy(out, l.records[start:])
	return out
}

// AuditCmd displays execution telemetry and audit history.
type AuditCmd struct {
	logger *AuditLogger
}

func NewAuditCmd(l *AuditLogger) *AuditCmd {
	if l == nil {
		l = DefaultAuditLogger()
	}
	return &AuditCmd{logger: l}
}

func (c *AuditCmd) Name() string        { return "audit" }
func (c *AuditCmd) Description() string { return "View execution audit logs: audit [limit]" }
func (c *AuditCmd) PipelineSafe() bool  { return true }

func (c *AuditCmd) Run(ctx registry.Context, args []string) error {
	records := c.logger.Recent(20)
	if len(records) == 0 {
		_, err := fmt.Fprintln(ctx.Output(), "(no audit records logged yet)")
		return err
	}

	tw := tabwriter.NewWriter(ctx.Output(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(ctx.Output(), "NexShell Audit Telemetry:")
	fmt.Fprintln(tw, "  TIME\tSTATUS\tDURATION\tRISK\tCOMMAND")
	fmt.Fprintln(tw, "  ────\t──────\t────────\t────\t───────")

	for _, r := range records {
		tStr := r.Timestamp.Format("15:04:05")
		fmt.Fprintf(tw, "  %s\t%d\t%s\t%s\t%s\n", tStr, r.ExitCode, r.Duration.Round(time.Millisecond), r.RiskLevel, r.Command)
	}
	return tw.Flush()
}
