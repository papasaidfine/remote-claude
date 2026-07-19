//go:build !windows

package platform

import (
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/papasaidfine/remote-claude/internal/paths"
)

// have reports whether cmd is on PATH.
func have(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}

// grepFile reports whether the file content matches the (multiline) pattern.
func grepFile(path, pattern string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	ok, _ := regexp.Match(pattern, b)
	return ok
}

// sshdManagedSettings returns the managed sshd settings block (drop-in body or
// directives applied when editing directly).
func sshdManagedSettings(disablePassword bool) string {
	s := "# Managed by reverse-ssh-bootstrap (" + paths.Alias + "). Delete this file to roll back.\n" +
		"PubkeyAuthentication yes\n" +
		"AuthorizedKeysFile .ssh/authorized_keys\n"
	if disablePassword {
		s += "PasswordAuthentication no\nKbdInteractiveAuthentication no\n"
	}
	return s
}

// useDropin reports whether sshd includes the sshd_config.d drop-in directory.
func useDropin(sshdConfig string) bool {
	if _, err := os.Stat("/etc/ssh"); err != nil {
		return false
	}
	return grepFile(sshdConfig, `(?m)^[ \t]*Include[ \t]+/etc/ssh/sshd_config\.d/\*`)
}

// sudoWriteFile writes content to dst as root, via a user-owned temp file and
// `sudo cp` (avoids needing a root shell just to redirect).
func sudoWriteFile(dst, content string) error {
	tmp, err := os.CreateTemp("", "rc-sshd-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	return run("sudo", "cp", tmp.Name(), dst)
}

// readMaybeSudo reads path, falling back to `sudo cat` when unreadable directly.
func readMaybeSudo(path string) (string, error) {
	if b, err := os.ReadFile(path); err == nil {
		return string(b), nil
	}
	out, err := runQuiet("sudo", "cat", path)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\n") + "\n", nil
}
