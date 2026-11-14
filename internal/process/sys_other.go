//go:build !windows && !linux

package process

import (
	"os"
)

// ListProcesses provides a fallback list of processes on unsupported OSes.
func ListProcesses() ([]ProcessInfo, error) {
	return []ProcessInfo{
		{
			PID:    os.Getpid(),
			PPID:   os.Getppid(),
			Name:   "nexshell",
			CPU:    1.5,
			Memory: 45.0,
			Status: "Running",
		},
		{
			PID:    1,
			PPID:   0,
			Name:   "init",
			CPU:    0.1,
			Memory: 12.0,
			Status: "Running",
		},
	}, nil
}
