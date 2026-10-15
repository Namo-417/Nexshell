//go:build linux

package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"nexshell/internal/parser"
	"nexshell/internal/registry"
)

// This helper runs in a separate session with a real controlling terminal.
// Ordinary pipe-based executor tests cannot detect SIGTTIN or a missing
// foreground-process-group restoration after a command finishes.
func TestLinuxTerminalHelper(t *testing.T) {
	mode := os.Getenv("NEXSHELL_TERMINAL_TEST")
	if mode == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var command parser.Command
	switch mode {
	case "read":
		command = fixture(t, "copy")
		fmt.Fprintln(os.Stdout, "terminal-driver-ready")
	case "interrupt":
		command = fixture(t, "tree", os.Getenv("NEXSHELL_TERMINAL_PID_BASE"))
	default:
		t.Fatalf("unexpected terminal test mode %q", mode)
	}
	status, err := Execute(ctx, parser.Plan{Commands: []parser.Command{command}}, registry.Context{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
	})
	if mode == "read" && (status != 0 || err != nil) {
		t.Fatalf("terminal read status=%d err=%v", status, err)
	}
	if mode == "interrupt" && (status != 130 || !errors.Is(err, context.Canceled)) {
		t.Fatalf("terminal interrupt status=%d err=%v", status, err)
	}
	if ctx.Err() != nil {
		t.Fatalf("terminal command reached its deadline: %v", ctx.Err())
	}
	group, err := terminalGroup(os.Stdin)
	if err != nil || group != int32(syscall.Getpgrp()) {
		t.Fatalf("shell did not regain terminal: group=%d shell=%d err=%v", group, syscall.Getpgrp(), err)
	}
	fmt.Fprintln(os.Stdout, "terminal-restored")
}

func TestLinuxForegroundTerminal(t *testing.T) {
	for _, mode := range []string{"read", "interrupt"} {
		t.Run(mode, func(t *testing.T) {
			master, slave := openLinuxPTY(t)
			defer master.Close()
			defer slave.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			pidBase := filepath.Join(t.TempDir(), "terminal-tree")
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestLinuxTerminalHelper$")
			cmd.Env = append(os.Environ(), "NEXSHELL_TERMINAL_TEST="+mode, "NEXSHELL_TERMINAL_PID_BASE="+pidBase)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			_ = slave.Close()
			completed := make(chan error, 1)
			go func() { completed <- cmd.Wait() }()
			chunks := make(chan string, 32)
			go func() {
				defer close(chunks)
				var buffer [4096]byte
				for {
					n, err := master.Read(buffer[:])
					if n > 0 {
						select {
						case chunks <- string(buffer[:n]):
						case <-ctx.Done():
							return
						}
					}
					if err != nil {
						return
					}
				}
			}()
			var output strings.Builder
			waitFor := func(marker string) {
				t.Helper()
				for !strings.Contains(output.String(), marker) {
					select {
					case chunk, ok := <-chunks:
						if !ok {
							t.Fatalf("terminal closed before %q; output=%q", marker, output.String())
						}
						output.WriteString(chunk)
					case <-ctx.Done():
						t.Fatalf("terminal timed out awaiting %q; output=%q", marker, output.String())
					}
				}
			}
			if mode == "read" {
				waitFor("terminal-driver-ready\r\n")
				// In canonical terminal mode Ctrl+D after a complete line gives
				// the foreground reader EOF without closing its terminal.
				if _, err := io.WriteString(master, "terminal-payload\n\x04"); err != nil {
					t.Fatal(err)
				}
				waitFor("terminal-payload\r\n")
			} else {
				waitFor("ready\r\n")
				// Writing the terminal's interrupt character signals its actual
				// foreground process group; this is not context cancellation.
				if _, err := master.Write([]byte{3}); err != nil {
					t.Fatal(err)
				}
			}
			waitFor("terminal-restored\r\n")
			if mode == "read" && strings.Count(output.String(), "terminal-payload\r\n") != 2 {
				t.Fatalf("expected terminal echo and child output; output=%q", output.String())
			}
			select {
			case err := <-completed:
				if err != nil {
					t.Fatalf("terminal driver failed: %v; output=%q", err, output.String())
				}
			case <-ctx.Done():
				t.Fatalf("terminal driver did not exit; output=%q", output.String())
			}
			if mode == "interrupt" {
				for _, suffix := range []string{".parent", ".child"} {
					content, err := os.ReadFile(pidBase + suffix)
					if err != nil {
						t.Fatal(err)
					}
					pid, err := strconv.Atoi(string(content))
					if err != nil {
						t.Fatal(err)
					}
					assertProcessStopped(t, pid)
				}
			}
		})
	}
}

func openLinuxPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
			t.Skipf("pseudo-terminal unavailable: %v", err)
		}
		t.Fatal(err)
	}
	var unlocked int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlocked))); errno != 0 {
		master.Close()
		t.Fatal(errno)
	}
	var number uint32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&number))); errno != 0 {
		master.Close()
		t.Fatal(errno)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		t.Fatal(err)
	}
	return master, slave
}
