//go:build windows

package proctree

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

const (
	createSuspended                                      = 0x00000004
	jobObjectExtendedLimitInformationClass               = 9
	jobObjectLimitKillOnJobClose                         = 0x00002000
	processSetQuota                                      = 0x00000100
	processTerminate                                     = 0x00000001
	threadSuspendResume                                  = 0x00000002
	th32csSnapThread                                     = 0x00000004
	errorNotFound                          syscall.Errno = 1168
)

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject       = kernel32.NewProc("TerminateJobObject")
	procCreateToolhelp32Snapshot = kernel32.NewProc("CreateToolhelp32Snapshot")
	procThread32First            = kernel32.NewProc("Thread32First")
	procThread32Next             = kernel32.NewProc("Thread32Next")
	procOpenThread               = kernel32.NewProc("OpenThread")
	procResumeThread             = kernel32.NewProc("ResumeThread")
)

// threadEntry32 mirrors THREADENTRY32; every field is 32 bits, so the Go and
// C layouts agree on every architecture.
type threadEntry32 struct {
	size           uint32
	usage          uint32
	threadID       uint32
	ownerProcessID uint32
	basePri        int32
	deltaPri       int32
	flags          uint32
}

// tree is a Job Object holding one command and all of its descendants.
type tree struct {
	job      syscall.Handle
	attached bool
}

// newTree creates the job and arranges for the child to be created suspended,
// so it cannot start a descendant before it is inside the job.
func newTree(cmd *exec.Cmd) (*tree, error) {
	job, _, callErr := procCreateJobObjectW.Call(0, 0)
	if job == 0 {
		return nil, fmt.Errorf("proctree: create job object: %w", callError(callErr))
	}
	t := &tree{job: syscall.Handle(job)}
	// JOBOBJECT_EXTENDED_LIMIT_INFORMATION is 144 bytes on 64-bit Windows and
	// 112 on 32-bit; LimitFlags sits at offset 16 in both. Written as bytes
	// because Go and MSVC disagree about 64-bit field alignment on 386, so a
	// Go struct would not have the C layout there.
	size := 112
	if unsafe.Sizeof(uintptr(0)) == 8 {
		size = 144
	}
	limits := make([]byte, size)
	binary.LittleEndian.PutUint32(limits[16:], jobObjectLimitKillOnJobClose)
	ok, _, callErr := procSetInformationJobObject.Call(
		job, jobObjectExtendedLimitInformationClass,
		uintptr(unsafe.Pointer(&limits[0])), uintptr(len(limits)))
	if ok == 0 {
		_ = t.release()
		return nil, fmt.Errorf("proctree: configure job object: %w", callError(callErr))
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createSuspended
	return t, nil
}

// attach assigns the suspended child to the job and then lets it run.
func (t *tree) attach(p *os.Process) error {
	handle, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(p.Pid))
	if err != nil {
		return fmt.Errorf("proctree: open process %d: %w", p.Pid, err)
	}
	defer syscall.CloseHandle(handle)
	ok, _, callErr := procAssignProcessToJobObject.Call(uintptr(t.job), uintptr(handle))
	if ok == 0 {
		return fmt.Errorf("proctree: assign process %d to job object: %w", p.Pid, callError(callErr))
	}
	t.attached = true
	if err := resumeProcess(p.Pid); err != nil {
		return fmt.Errorf("proctree: resume process %d: %w", p.Pid, err)
	}
	return nil
}

// kill terminates every process in the job, or only the root when it was
// never attached (it is then still suspended and has started nothing).
func (t *tree) kill(p *os.Process) error {
	if t.attached && t.job != 0 {
		ok, _, callErr := procTerminateJobObject.Call(uintptr(t.job), 1)
		if ok != 0 {
			return nil
		}
		jobErr := callError(callErr)
		if err := p.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return errors.Join(fmt.Errorf("proctree: terminate job object: %w", jobErr), err)
		}
		return nil
	}
	return p.Kill()
}

// release terminates any member still alive and closes the job. Closing the
// last handle of a KILL_ON_JOB_CLOSE job terminates its members by itself;
// terminating first makes that explicit and independent of handle counting.
func (t *tree) release() error {
	if t.job == 0 {
		return nil
	}
	var errs []error
	if t.attached {
		if ok, _, callErr := procTerminateJobObject.Call(uintptr(t.job), 1); ok == 0 {
			if err := callError(callErr); !errors.Is(err, errorNotFound) {
				errs = append(errs, fmt.Errorf("proctree: terminate job object: %w", err))
			}
		}
	}
	if err := syscall.CloseHandle(t.job); err != nil {
		errs = append(errs, fmt.Errorf("proctree: close job object: %w", err))
	}
	t.job = 0
	t.attached = false
	return errors.Join(errs...)
}

// resumeProcess resumes the single, suspended thread of a newly created
// process. os.StartProcess does not expose the thread handle CreateProcess
// returned, so the thread is found through a Toolhelp snapshot.
func resumeProcess(pid int) error {
	snapshot, _, callErr := procCreateToolhelp32Snapshot.Call(th32csSnapThread, 0)
	if snapshot == uintptr(syscall.InvalidHandle) {
		return fmt.Errorf("snapshot threads: %w", callError(callErr))
	}
	defer syscall.CloseHandle(syscall.Handle(snapshot))

	entry := threadEntry32{size: uint32(unsafe.Sizeof(threadEntry32{}))}
	ok, _, callErr := procThread32First.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
	for ok != 0 {
		if entry.ownerProcessID == uint32(pid) {
			thread, _, openErr := procOpenThread.Call(threadSuspendResume, 0, uintptr(entry.threadID))
			if thread == 0 {
				return fmt.Errorf("open thread %d: %w", entry.threadID, callError(openErr))
			}
			previous, _, resumeErr := procResumeThread.Call(thread)
			_ = syscall.CloseHandle(syscall.Handle(thread))
			// ResumeThread returns a DWORD; (DWORD)-1 reports failure. The
			// uintptr holds it zero-extended on 64-bit, so compare 32 bits.
			if uint32(previous) == 0xFFFFFFFF {
				return fmt.Errorf("resume thread %d: %w", entry.threadID, callError(resumeErr))
			}
			return nil
		}
		entry.size = uint32(unsafe.Sizeof(threadEntry32{}))
		ok, _, callErr = procThread32Next.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
	}
	return fmt.Errorf("no thread found for process %d: %w", pid, callError(callErr))
}

func callError(err error) error {
	if err == nil || errors.Is(err, syscall.Errno(0)) {
		return errors.New("Windows API call failed")
	}
	return err
}
