//go:build !windows

package relay

import (
	"os/exec"
	"syscall"
)

// setSysProcAttr puts xray in its own process group so we can kill the whole
// tree on disconnect.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// afterStart is a no-op on Unix (the process group is set at start).
func afterStart(cmd *exec.Cmd) {}

// kill terminates xray and its process group.
func kill(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	// Negative pid targets the whole process group.
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	cmd.Process.Kill()
	cmd.Wait()
}
