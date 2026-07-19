// Package paths resolves every file location the tool touches, per operating
// system, so the rest of the code stays platform-neutral.
package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

// Alias is the fixed ssh Host alias and managed-block name.
const Alias = "remote-claude"

// KeyName is the default local key filename (local → server hop).
const KeyName = "id_ed25519"

// Managed-block markers. These MUST stay byte-identical to the strings the
// original bootstrap scripts wrote, so existing ~/.ssh/config blocks keep
// being recognized.
const (
	BeginMark = "# >>> " + Alias + " (managed by reverse-ssh-bootstrap) >>>"
	EndMark   = "# <<< " + Alias + " <<<"
)

// Paths holds the resolved absolute locations for the current user/OS.
type Paths struct {
	SSHDir      string // ~/.ssh
	KeyPath     string // ~/.ssh/id_ed25519
	SSHConfig   string // ~/.ssh/config
	AuthKeys    string // ~/.ssh/authorized_keys
	RCConfigDir string // per-OS config dir for xray + nodes
	XrayBin     string // vendored xray(.exe)
	VlessNodes  string // vless-nodes.txt
}

// Resolve computes the paths for the current user and OS.
func Resolve() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	sshDir := filepath.Join(home, ".ssh")
	rc := configDir(home)
	xrayName := "xray"
	if runtime.GOOS == "windows" {
		xrayName = "xray.exe"
	}
	return Paths{
		SSHDir:      sshDir,
		KeyPath:     filepath.Join(sshDir, KeyName),
		SSHConfig:   filepath.Join(sshDir, "config"),
		AuthKeys:    filepath.Join(sshDir, "authorized_keys"),
		RCConfigDir: rc,
		XrayBin:     filepath.Join(rc, "bin", xrayName),
		VlessNodes:  filepath.Join(rc, "vless-nodes.txt"),
	}, nil
}

// configDir is %LOCALAPPDATA%\remote-claude on Windows, ~/.config/remote-claude
// elsewhere — matching the paths the old scripts used.
func configDir(home string) string {
	if runtime.GOOS == "windows" {
		if lad := os.Getenv("LOCALAPPDATA"); lad != "" {
			return filepath.Join(lad, Alias)
		}
		return filepath.Join(home, "AppData", "Local", Alias)
	}
	return filepath.Join(home, ".config", Alias)
}

// SelfExe returns the absolute path of the running binary, used to point the
// ssh ProxyCommand at "<self> relay %h %p".
func SelfExe() string {
	p, err := os.Executable()
	if err != nil {
		return Alias
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}
