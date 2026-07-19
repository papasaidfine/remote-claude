//go:build windows

package relay

import (
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// jobObjectExtendedLimitInformation is the JobObjectInformationClass value for
// JOBOBJECT_EXTENDED_LIMIT_INFORMATION.
const jobObjectExtendedLimitInformation = 9

// jobHandle is kept open for the life of the relay; when the relay exits (or is
// hard-killed) the handle closes and the kill-on-job-close job terminates xray.
var jobHandle windows.Handle

// setSysProcAttr starts xray without popping a console window.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
}

// afterStart binds xray to a kill-on-close Job Object so it cannot outlive the
// relay (belt-and-suspenders alongside the explicit kill()).
func afterStart(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		windows.CloseHandle(job)
		return
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		windows.CloseHandle(job)
		return
	}
	jobHandle = job // deliberately left open
}

// kill terminates xray directly.
func kill(cmd *exec.Cmd) {
	if cmd.Process != nil {
		cmd.Process.Kill()
		cmd.Wait()
	}
}
