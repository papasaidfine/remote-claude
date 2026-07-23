// Package keys ensures the local ssh key (local → server hop) exists and
// returns its public half.
package keys

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/sshbin"
	"github.com/papasaidfine/remote-claude/internal/sysproc"
)

// SetPerms tightens permissions on a file or directory (chmod / icacls).
type SetPerms func(path string, isDir bool) error

// EnsureSSHDir creates ~/.ssh and authorized_keys with strict permissions.
func EnsureSSHDir(p paths.Paths, setPerms SetPerms) error {
	if err := os.MkdirAll(p.SSHDir, 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(p.AuthKeys); os.IsNotExist(err) {
		if err := os.WriteFile(p.AuthKeys, nil, 0o600); err != nil {
			return err
		}
	}
	if setPerms != nil {
		if err := setPerms(p.SSHDir, true); err != nil {
			return err
		}
		if err := setPerms(p.AuthKeys, false); err != nil {
			return err
		}
	}
	return nil
}

// Result reports what Ensure did.
type Result struct {
	Pub        string // public key text
	Generated  bool   // a new key was created
	Passphrase bool   // the existing key looks passphrase-protected
}

// Ensure makes sure the default ed25519 key exists, deriving or generating as
// needed, and returns its public key. warn is called for non-fatal notices.
func Ensure(p paths.Paths, setPerms SetPerms) (Result, error) {
	if err := EnsureSSHDir(p, setPerms); err != nil {
		return Result{}, err
	}
	keygen := sshbin.Keygen()
	var res Result

	if _, err := os.Stat(p.KeyPath); err == nil {
		// Key exists: probe with an empty passphrase; success means unprotected.
		probe := exec.Command(keygen, "-y", "-P", "", "-f", p.KeyPath)
		sysproc.Hide(probe)
		out, probeErr := probe.Output()
		pubPath := p.KeyPath + ".pub"
		if _, statErr := os.Stat(pubPath); os.IsNotExist(statErr) {
			if probeErr != nil {
				return Result{}, fmt.Errorf("%s exists but %s is missing and could not be derived (passphrase-protected?); please fix and re-run", p.KeyPath, pubPath)
			}
			if err := os.WriteFile(pubPath, append(trimTrailingNewlines(out), '\n'), 0o644); err != nil {
				return Result{}, err
			}
		}
		res.Passphrase = probeErr != nil
	} else {
		if err := runKeygen(keygen, "-t", "ed25519", "-f", p.KeyPath, "-N", ""); err != nil {
			return Result{}, fmt.Errorf("ssh-keygen failed: %w", err)
		}
		res.Generated = true
	}
	if setPerms != nil {
		if err := setPerms(p.KeyPath, false); err != nil {
			return Result{}, err
		}
	}
	pub, err := os.ReadFile(p.KeyPath + ".pub")
	if err != nil {
		return Result{}, err
	}
	res.Pub = string(pub)
	return res, nil
}

// ValidatePub returns a validator (for authorize.Add) that runs ssh-keygen -lf.
func ValidatePub() func(pub string) error {
	return func(pub string) error {
		tmp, err := os.CreateTemp("", "rc-pub-*")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		if _, err := tmp.WriteString(pub + "\n"); err != nil {
			tmp.Close()
			return err
		}
		tmp.Close()
		cmd := exec.Command(sshbin.Keygen(), "-lf", tmp.Name())
		sysproc.Hide(cmd)
		return cmd.Run()
	}
}

func runKeygen(bin string, args ...string) error {
	cmd := exec.Command(bin, args...)
	sysproc.Hide(cmd)
	cmd.Stdout = nil
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func trimTrailingNewlines(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
