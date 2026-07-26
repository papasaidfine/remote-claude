// Package platform abstracts the OS-specific operations the menu needs:
// strict file permissions, installing/hardening the incoming sshd, and the
// elevation model. Each OS provides its own build-tagged implementation.
package platform

import (
	"os"
	"os/exec"
	"time"

	"github.com/papasaidfine/remote-claude/internal/sysproc"
)

// Platform is the set of OS-divergent operations.
type Platform interface {
	// Name is a short OS label ("Windows", "macOS", "Linux").
	Name() string
	// SupportsXray reports whether the optional xray phase is offered.
	SupportsXray() bool
	// SetStrictPerms tightens permissions on a file or directory.
	SetStrictPerms(path string, isDir bool) error
	// EnsureIncomingSSH installs/enables and hardens the local sshd so the
	// agent can connect back. disablePassword turns off password auth.
	EnsureIncomingSSH(disablePassword bool) error
	// StatusIncomingSSH reports whether sshd is running and hardened.
	StatusIncomingSSH() bool
	// RequireElevation returns a non-nil error (with a re-run hint) when the
	// incoming-SSH item needs privileges not currently held; nil otherwise.
	RequireElevation() error
	// OpenURL hands a URL to the desktop's default handler. Used for the
	// vscode:// deep link, so which editor answers it is the OS's business.
	OpenURL(url string) error
}

// New returns the Platform implementation for the current OS.
func New() Platform { return newPlatform() }

// command builds an exec.Cmd for an OS helper with its console window suppressed.
// The GUI runs windowless (built -H=windowsgui, so it owns no console); without
// this, every console-subsystem child it spawns — icacls, powershell, sshd -t —
// allocates and flashes its own black window. Local setup fires icacls four times
// per SetStrictPerms and several times per run, so the flashes read as a swarm.
// sysproc.Hide is a no-op off Windows, so the CLI's interactive sudo prompts in
// run() keep their terminal.
func command(bin string, args ...string) *exec.Cmd {
	cmd := exec.Command(bin, args...)
	sysproc.Hide(cmd)
	return cmd
}

// run executes a command with its output attached to the console.
func run(bin string, args ...string) error {
	cmd := command(bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// runQuiet executes a command and captures its combined output.
func runQuiet(bin string, args ...string) ([]byte, error) {
	return command(bin, args...).CombinedOutput()
}

// timestamp is the suffix used for backup files.
func timestamp() string { return time.Now().Format("20060102-150405") }
