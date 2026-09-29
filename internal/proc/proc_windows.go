//go:build windows

package proc

import (
	"math"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type sysGroup struct{ job windows.Handle }

// newSysGroup creates a job object that kills its processes when the last
// handle to it closes: when llamaenv exits, even from a hard kill.
func newSysGroup() (sysGroup, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return sysGroup{}, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	// G103: the Windows API takes a pointer to info, which stays alive for the call.
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil { //nolint:gosec // Windows API call, see above
		_ = windows.CloseHandle(job)
		return sysGroup{}, err
	}
	return sysGroup{job: job}, nil
}

// prepare hides console windows of the servers.
func prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
}

func (g sysGroup) add(cmd *exec.Cmd) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid32(cmd.Process.Pid))
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	return windows.AssignProcessToJobObject(g.job, h)
}

// terminate stops a server at once: Windows console programs have no
// portable graceful stop from another process, and llama.cpp keeps no state
// that a hard stop would lose.
func terminate(c *exec.Cmd) {
	if c.Process != nil {
		_ = c.Process.Kill()
	}
}

func alive(c *exec.Cmd) bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid32(c.Process.Pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	ev, _ := windows.WaitForSingleObject(h, 0)
	return ev == uint32(windows.WAIT_TIMEOUT)
}

func (g sysGroup) killAll([]*exec.Cmd) {
	_ = windows.TerminateJobObject(g.job, 1)
}

// pid32 converts a process ID for the Windows API. Windows process IDs are
// 32-bit, so an out-of-range value cannot be a real process.
func pid32(pid int) uint32 {
	if pid < 0 || pid > math.MaxUint32 {
		return 0
	}
	return uint32(pid)
}

// Watch is only needed on macOS.
func Watch([]string) int { return 2 }
