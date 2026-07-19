// Package sshbin resolves the OpenSSH client binaries per OS. On Windows they
// live under %SystemRoot%\System32\OpenSSH; elsewhere they are on PATH.
package sshbin

import (
	"os"
	"path/filepath"
	"runtime"
)

func win(name string) string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	p := filepath.Join(root, "System32", "OpenSSH", name)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return name // fall back to PATH
}

// Keygen returns the ssh-keygen binary.
func Keygen() string {
	if runtime.GOOS == "windows" {
		return win("ssh-keygen.exe")
	}
	return "ssh-keygen"
}

// SSH returns the ssh client binary.
func SSH() string {
	if runtime.GOOS == "windows" {
		return win("ssh.exe")
	}
	return "ssh"
}
