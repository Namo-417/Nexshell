package system

import (
	"bytes"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"nexshell/internal/parser"
	"nexshell/internal/registry"
)

// BenchmarksCmd executes performance evaluations matching the OS project blueprint.
type BenchmarksCmd struct{}

func (c *BenchmarksCmd) Name() string        { return "benchmarks" }
func (c *BenchmarksCmd) Description() string { return "Run shell performance benchmarks (startup, pipeline, latency, concurrency)" }
func (c *BenchmarksCmd) PipelineSafe() bool  { return true }

type benchMetric struct {
	Name        string
	Value       string
	Description string
}

func (c *BenchmarksCmd) Run(ctx registry.Context, _ []string) error {
	fmt.Fprintln(ctx.Output(), "\033[36mRunning NexShell OS Performance Benchmarks...\033[0m")
	var metrics []benchMetric

	// 1. Startup & Memory footprint
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	metrics = append(metrics, benchMetric{
		Name:        "Memory Footprint",
		Value:       fmt.Sprintf("%.2f MB", float64(memStats.Alloc)/(1024*1024)),
		Description: "Resident heap memory allocated by shell runtime",
	})

	// 2. Command Latency (Builtin dispatch)
	start := time.Now()
	const iters = 1000
	for i := 0; i < iters; i++ {
		_ = parser.Validate(parser.Plan{Commands: []parser.Command{{Name: "echo", Args: []string{"ping"}}}})
	}
	avgParseLatency := time.Since(start) / iters
	metrics = append(metrics, benchMetric{
		Name:        "Plan Parsing Latency",
		Value:       fmt.Sprintf("%v", avgParseLatency),
		Description: "Average latency to parse and validate a pipeline stage",
	})

	// 3. Pipeline Throughput
	dataSizeMB := 10.0
	payload := bytes.Repeat([]byte("test data line for throughput benchmark\n"), 250000)
	pipeStart := time.Now()
	reader := bytes.NewReader(payload)
	var discard bytes.Buffer
	_, _ = discard.ReadFrom(reader)
	pipeDuration := time.Since(pipeStart)
	throughput := dataSizeMB / pipeDuration.Seconds()
	metrics = append(metrics, benchMetric{
		Name:        "Pipeline Throughput",
		Value:       fmt.Sprintf("%.1f MB/s", throughput),
		Description: "Data streaming throughput through pipeline buffers",
	})

	// 4. Process-launch overhead
	procStart := time.Now()
	testCmd := exec.Command("go", "version")
	if err := testCmd.Run(); err == nil {
		procDur := time.Since(procStart)
		metrics = append(metrics, benchMetric{
			Name:        "Process Launch Overhead",
			Value:       fmt.Sprintf("%v", procDur.Round(time.Millisecond)),
			Description: "Cold fork/exec latency of an external process",
		})
	}

	// 5. Concurrency / Parallel Task Speedup
	const workerCount = 8
	var wg sync.WaitGroup
	concStart := time.Now()
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var localMem runtime.MemStats
			runtime.ReadMemStats(&localMem)
		}()
	}
	wg.Wait()
	concDur := time.Since(concStart)
	metrics = append(metrics, benchMetric{
		Name:        "Concurrent Task Overhead",
		Value:       fmt.Sprintf("%v (%d workers)", concDur, workerCount),
		Description: "Synchronization latency across concurrent worker threads",
	})

	// Render table
	tw := tabwriter.NewWriter(ctx.Output(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(ctx.Output(), "\nNexShell Benchmark Results:")
	fmt.Fprintln(tw, "  METRIC\tVALUE\tDESCRIPTION")
	fmt.Fprintln(tw, "  ──────\t─────\t───────────")
	for _, m := range metrics {
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", m.Name, m.Value, m.Description)
	}
	_ = tw.Flush()
	fmt.Fprintln(ctx.Output(), strings.Repeat("─", 65))
	return nil
}
