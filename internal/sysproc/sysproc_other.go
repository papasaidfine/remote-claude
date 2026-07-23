//go:build !windows

// Package sysproc adjusts child-process attributes per OS.
package sysproc

import "os/exec"

// Hide is a no-op off Windows — there are no console windows to suppress.
func Hide(cmd *exec.Cmd) {}
