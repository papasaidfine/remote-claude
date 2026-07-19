//go:build linux

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/papasaidfine/remote-claude/internal/sshdconf"
	"github.com/papasaidfine/remote-claude/internal/ui"
)

const (
	linSshdConfig = "/etc/ssh/sshd_config"
	linSshdDropin = "/etc/ssh/sshd_config.d/100-remote-claude.conf"
)

type linuxPlatform struct{}

func newPlatform() Platform { return linuxPlatform{} }

func (linuxPlatform) Name() string       { return "Linux" }
func (linuxPlatform) SupportsXray() bool { return false }
func (linuxPlatform) RequireElevation() error {
	return nil // item 4 uses sudo per-command
}

func (linuxPlatform) SetStrictPerms(path string, isDir bool) error {
	if isDir {
		return os.Chmod(path, 0o700)
	}
	return os.Chmod(path, 0o600)
}

func findSshdBin() string {
	for _, p := range []string{"/usr/sbin/sshd", "/usr/bin/sshd", "/usr/local/sbin/sshd"} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	if p, err := exec.LookPath("sshd"); err == nil {
		return p
	}
	return ""
}

func hasSystemd() bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	_, err := os.Stat("/run/systemd/system")
	return err == nil
}

func findSshdUnit() string {
	for _, u := range []string{"sshd", "ssh"} {
		out, _ := runQuiet("systemctl", "list-unit-files", u+".service", "--no-legend")
		if strings.Contains(string(out), u+".service") {
			return u
		}
	}
	return ""
}

func (linuxPlatform) StatusIncomingSSH() bool {
	if findSshdBin() == "" {
		return false
	}
	if hasSystemd() {
		unit := findSshdUnit()
		if unit == "" {
			return false
		}
		if err := exec.Command("systemctl", "is-active", "--quiet", unit+".service").Run(); err != nil {
			return false
		}
	}
	if _, err := os.Stat(linSshdDropin); err == nil {
		return true
	}
	return grepFile(linSshdConfig, `(?m)^[ \t]*PubkeyAuthentication[ \t]+yes`)
}

func (linuxPlatform) EnsureIncomingSSH(disablePassword bool) error {
	if findSshdBin() == "" {
		if err := installOpenSSHServer(); err != nil {
			return err
		}
	}
	if findSshdBin() == "" {
		return fmt.Errorf("openssh-server installation did not provide an sshd binary")
	}
	unit := ""
	if hasSystemd() {
		unit = findSshdUnit()
	}
	if unit != "" {
		ui.Log("Enabling and starting %s.service", unit)
		if err := run("sudo", "systemctl", "enable", "--now", unit+".service"); err != nil {
			return err
		}
	} else {
		ui.Warn("systemd not detected; please make sure sshd is running and enabled with your init system")
	}
	return configureLinuxSshd(disablePassword, unit)
}

func installOpenSSHServer() error {
	ui.Log("Installing openssh-server")
	switch {
	case have("apt-get"):
		if err := run("sudo", "apt-get", "update", "-qq"); err != nil {
			return err
		}
		return run("sudo", "apt-get", "install", "-y", "openssh-server")
	case have("dnf"):
		return run("sudo", "dnf", "install", "-y", "openssh-server")
	case have("yum"):
		return run("sudo", "yum", "install", "-y", "openssh-server")
	case have("pacman"):
		return run("sudo", "pacman", "-S", "--noconfirm", "openssh")
	case have("zypper"):
		return run("sudo", "zypper", "install", "-y", "openssh")
	default:
		return fmt.Errorf("no supported package manager found (apt/dnf/yum/pacman/zypper); install openssh-server manually and re-run")
	}
}

func configureLinuxSshd(disablePassword bool, unit string) error {
	ts := timestamp()
	settings := sshdManagedSettings(disablePassword)

	if useDropin(linSshdConfig) {
		ui.Log("Writing sshd drop-in config: %s", linSshdDropin)
		if _, err := os.Stat(linSshdDropin); err == nil {
			_ = run("sudo", "cp", linSshdDropin, linSshdDropin+".claude-bak-"+ts)
		}
		if err := sudoWriteFile(linSshdDropin, settings); err != nil {
			return err
		}
		if err := sudoSshdCheck(); err != nil {
			ui.Errf("sshd config validation failed, rolling back the drop-in")
			_ = run("sudo", "rm", "-f", linSshdDropin)
			return err
		}
	} else {
		ui.Log("sshd_config.d not supported here, editing %s directly (backed up)", linSshdConfig)
		if err := run("sudo", "cp", linSshdConfig, linSshdConfig+".claude-bak-"+ts); err != nil {
			return err
		}
		text, err := readMaybeSudo(linSshdConfig)
		if err != nil {
			return err
		}
		text = sshdconf.SetDirective(text, "PubkeyAuthentication", "yes")
		text = sshdconf.SetDirective(text, "AuthorizedKeysFile", ".ssh/authorized_keys")
		if disablePassword {
			text = sshdconf.SetDirective(text, "PasswordAuthentication", "no")
			text = sshdconf.SetDirective(text, "KbdInteractiveAuthentication", "no")
		}
		if err := sudoWriteFile(linSshdConfig, text); err != nil {
			return err
		}
		if err := sudoSshdCheck(); err != nil {
			ui.Errf("sshd config validation failed, restoring the backup")
			_ = run("sudo", "cp", linSshdConfig+".claude-bak-"+ts, linSshdConfig)
			return err
		}
	}
	ui.Log("sshd config validation passed")
	if unit != "" {
		if err := run("sudo", "systemctl", "restart", unit+".service"); err != nil {
			return err
		}
		ui.Log("sshd restarted")
	} else {
		ui.Warn("Please restart sshd manually to apply the new configuration")
	}
	return nil
}

func sudoSshdCheck() error {
	bin := findSshdBin()
	out, err := runQuiet("sudo", bin, "-t")
	if len(out) > 0 {
		os.Stderr.Write(out)
	}
	return err
}
