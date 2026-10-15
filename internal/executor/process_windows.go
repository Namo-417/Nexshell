//go:build windows

package executor

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

// A job owns the entire foreground process tree. Starting suspended prevents a
// command from spawning children before assignment to the job. Windows 8+ can
// nest this job inside a job that already contains NexShell.
type processSet struct {
	job       syscall.Handle
	processes []*os.Process
}

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	createJobObject          = kernel32.NewProc("CreateJobObjectW")
	setInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	assignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	terminateJobObject       = kernel32.NewProc("TerminateJobObject")
	thread32First            = kernel32.NewProc("Thread32First")
	thread32Next             = kernel32.NewProc("Thread32Next")
	openThread               = kernel32.NewProc("OpenThread")
	resumeThread             = kernel32.NewProc("ResumeThread")
)

func newProcessSet(_ io.Reader) (*processSet, error) {
	handle, _, err := createJobObject.Call(0, 0)
	if handle == 0 {
		return nil, fmt.Errorf("CreateJobObject: %w", err)
	}
	set := &processSet{job: syscall.Handle(handle)}
	// JOBOBJECT_EXTENDED_LIMIT_INFORMATION has LimitFlags at byte 16 on
	// both Windows ABIs. Its native size is 144 (64-bit) or 112 (32-bit).
	// Only JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE is set; all limits stay zero.
	var information [144]byte
	binary.LittleEndian.PutUint32(information[16:20], 0x2000)
	size := uintptr(144)
	if unsafe.Sizeof(uintptr(0)) == 4 {
		size = 112
	}
	ok, _, err := setInformationJobObject.Call(handle, 9, uintptr(unsafe.Pointer(&information[0])), size)
	if ok == 0 {
		_ = syscall.CloseHandle(set.job)
		return nil, fmt.Errorf("SetInformationJobObject: %w", err)
	}
	return set, nil
}

func (s *processSet) prepare(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000004} // CREATE_SUSPENDED
	return nil
}

func (s *processSet) track(cmd *exec.Cmd) error {
	s.processes = append(s.processes, cmd.Process)
	handle, err := syscall.OpenProcess(0x0100|syscall.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return fmt.Errorf("OpenProcess for job assignment: %w", err)
	}
	defer syscall.CloseHandle(handle)
	ok, _, err := assignProcessToJobObject.Call(uintptr(s.job), uintptr(handle))
	if ok == 0 {
		return fmt.Errorf("AssignProcessToJobObject: %w", err)
	}
	return resumeInitialThread(uint32(cmd.Process.Pid))
}

type threadEntry struct {
	Size, Usage, ID, OwnerProcessID uint32
	BasePriority, DeltaPriority     int32
	Flags                           uint32
}

func resumeInitialThread(pid uint32) error {
	snapshot, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("snapshot initial thread: %w", err)
	}
	defer syscall.CloseHandle(snapshot)
	entry := threadEntry{Size: uint32(unsafe.Sizeof(threadEntry{}))}
	ok, _, callErr := thread32First.Call(uintptr(snapshot), uintptr(unsafe.Pointer(&entry)))
	for ok != 0 {
		if entry.OwnerProcessID == pid {
			handle, _, err := openThread.Call(0x0002, 0, uintptr(entry.ID)) // THREAD_SUSPEND_RESUME
			if handle == 0 {
				return fmt.Errorf("OpenThread: %w", err)
			}
			defer syscall.CloseHandle(syscall.Handle(handle))
			previous, _, err := resumeThread.Call(handle)
			if uint32(previous) == ^uint32(0) {
				return fmt.Errorf("ResumeThread: %w", err)
			}
			return nil
		}
		entry.Size = uint32(unsafe.Sizeof(threadEntry{}))
		ok, _, callErr = thread32Next.Call(uintptr(snapshot), uintptr(unsafe.Pointer(&entry)))
	}
	if callErr != syscall.ERROR_NO_MORE_FILES {
		return fmt.Errorf("enumerate initial thread: %w", callErr)
	}
	return fmt.Errorf("initial thread not found for process %d", pid)
}

func (s *processSet) kill() {
	_, _, _ = terminateJobObject.Call(uintptr(s.job), 130)
	// Also cover a process whose job assignment failed while suspended.
	for _, process := range s.processes {
		_ = process.Kill()
	}
}

func (s *processSet) close() error {
	// Kill-on-close also removes any descendants outliving their command.
	return syscall.CloseHandle(s.job)
}

func processExitCode(err *exec.ExitError) int     { return err.ExitCode() }
func processInterrupted(err *exec.ExitError) bool { return false }
