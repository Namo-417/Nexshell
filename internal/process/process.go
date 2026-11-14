package process

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"nexshell/internal/pipeline"
	"nexshell/internal/registry"
)

// ProcessInfo holds runtime metrics for an operating system process.
type ProcessInfo struct {
	PID    int     `json:"pid"`
	PPID   int     `json:"ppid"`
	Name   string  `json:"name"`
	CPU    float64 `json:"cpu"`
	Memory float64 `json:"memory"` // Memory in MB
	Status string  `json:"status"`
}

// ProcessesCmd lists system processes as structured pipeline objects.
// e.g.: `processes` or `processes | where cpu > 50 | select pid,name,cpu`
type ProcessesCmd struct{}

func (c *ProcessesCmd) Name() string        { return "processes" }
func (c *ProcessesCmd) Description() string { return "Inspect system processes: processes [filter]" }
func (c *ProcessesCmd) PipelineSafe() bool  { return true }

func (c *ProcessesCmd) Run(ctx registry.Context, args []string) error {
	procs, err := ListProcesses()
	if err != nil {
		return fmt.Errorf("processes: %w", err)
	}

	filterName := ""
	if len(args) > 0 {
		filterName = strings.ToLower(args[0])
	}

	var ds pipeline.Dataset
	for _, p := range procs {
		if filterName != "" && !strings.Contains(strings.ToLower(p.Name), filterName) {
			continue
		}
		rec := pipeline.Record{
			"pid":    p.PID,
			"ppid":   p.PPID,
			"name":   p.Name,
			"cpu":    p.CPU,
			"memory": p.Memory,
			"status": p.Status,
		}
		ds = append(ds, rec)
	}

	// Sort by PID by default
	sort.Slice(ds, func(i, j int) bool {
		pidI, _ := ds[i]["pid"].(int)
		pidJ, _ := ds[j]["pid"].(int)
		return pidI < pidJ
	})

	// If -json flag was provided, write JSON
	for _, a := range args {
		if a == "-json" || a == "--json" {
			return pipeline.WriteOutput(ctx.Output(), ds, true)
		}
	}

	// Render table by default
	return pipeline.RenderTable(ctx.Output(), ds, []string{"pid", "ppid", "name", "cpu", "memory", "status"})
}

func isTerminal(w io.Writer) bool {
	// If w is nil or os.Stdout, it's the final terminal output
	return w == nil || w == os.Stdout
}
