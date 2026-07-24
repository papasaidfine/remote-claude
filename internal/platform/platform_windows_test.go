//go:build windows

package platform

import (
	"testing"

	"golang.org/x/sys/windows"
)

// TestCommandHidesConsoleWindow guards the fix for the "Set up server" flashing:
// every OS helper the windowless GUI spawns (icacls, powershell, sshd -t) must be
// created with CREATE_NO_WINDOW, or each pops its own black console window.
func TestCommandHidesConsoleWindow(t *testing.T) {
	cmd := command("icacls")
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr is nil; command() must apply sysproc.Hide")
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Error("CREATE_NO_WINDOW not set; the console window will flash on Windows")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Error("HideWindow not set")
	}
}
