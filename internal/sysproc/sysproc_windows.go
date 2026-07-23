//go:build windows

// Package sysproc adjusts child-process attributes per OS.
package sysproc

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// Hide configures cmd so that starting it does not pop up a console window.
// ssh.exe and xray.exe are console-subsystem binaries; without this, every
// spawn flashes a black terminal even though the GUI itself runs windowless.
func Hide(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
}
