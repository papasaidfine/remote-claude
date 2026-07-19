//go:build darwin

package platform

import (
	"net"
	"os"
	"strings"
	"time"

	"github.com/papasaidfine/remote-claude/internal/sshdconf"
	"github.com/papasaidfine/remote-claude/internal/ui"
)

const (
	macSshdConfig = "/etc/ssh/sshd_config"
	macSshdDropin = "/etc/ssh/sshd_config.d/100-remote-claude.conf"
	macSshdBin    = "/usr/sbin/sshd"
)

type darwinPlatform struct{}

func newPlatform() Platform { return darwinPlatform{} }

func (darwinPlatform) Name() string            { return "macOS" }
func (darwinPlatform) SupportsXray() bool      { return true }
func (darwinPlatform) RequireElevation() error { return nil }

func (darwinPlatform) SetStrictPerms(path string, isDir bool) error {
	if isDir {
		return os.Chmod(path, 0o700)
	}
	return os.Chmod(path, 0o600)
}

func (darwinPlatform) StatusIncomingSSH() bool {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:22", 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	if _, err := os.Stat(macSshdDropin); err == nil {
		return true
	}
	return grepFile(macSshdConfig, `(?m)^[ \t]*PubkeyAuthentication[ \t]+yes`)
}

func (darwinPlatform) EnsureIncomingSSH(disablePassword bool) error {
	if err := enableRemoteLogin(); err != nil {
		return err
	}
	return configureMacSshd(disablePassword)
}

func enableRemoteLogin() error {
	out, _ := runQuiet("sudo", "systemsetup", "-getremotelogin")
	if strings.Contains(string(out), ": On") {
		ui.Log("Remote Login is already enabled")
		return nil
	}
	ui.Log("Enabling Remote Login (sshd)")
	if err := run("sudo", "systemsetup", "-setremotelogin", "on"); err != nil {
		ui.Warn("systemsetup failed (recent macOS may require Full Disk Access).")
		ui.Warn("Enable it manually: System Settings -> General -> Sharing -> Remote Login, then re-run.")
		return err
	}
	return nil
}

func configureMacSshd(disablePassword bool) error {
	ts := timestamp()
	settings := sshdManagedSettings(disablePassword)

	if useDropin(macSshdConfig) {
		ui.Log("Writing sshd drop-in config: %s", macSshdDropin)
		if _, err := os.Stat(macSshdDropin); err == nil {
			_ = run("sudo", "cp", macSshdDropin, macSshdDropin+".claude-bak-"+ts)
		}
		if err := sudoWriteFile(macSshdDropin, settings); err != nil {
			return err
		}
		if err := macSshdCheck(); err != nil {
			ui.Errf("sshd config validation failed, rolling back the drop-in")
			_ = run("sudo", "rm", "-f", macSshdDropin)
			return err
		}
	} else {
		ui.Log("sshd_config.d not supported here, editing %s directly (backed up)", macSshdConfig)
		if err := run("sudo", "cp", macSshdConfig, macSshdConfig+".claude-bak-"+ts); err != nil {
			return err
		}
		text, err := readMaybeSudo(macSshdConfig)
		if err != nil {
			return err
		}
		text = sshdconf.SetDirective(text, "PubkeyAuthentication", "yes")
		text = sshdconf.SetDirective(text, "AuthorizedKeysFile", ".ssh/authorized_keys")
		if disablePassword {
			text = sshdconf.SetDirective(text, "PasswordAuthentication", "no")
			text = sshdconf.SetDirective(text, "KbdInteractiveAuthentication", "no")
		}
		if err := sudoWriteFile(macSshdConfig, text); err != nil {
			return err
		}
		if err := macSshdCheck(); err != nil {
			ui.Errf("sshd config validation failed, restoring the backup")
			_ = run("sudo", "cp", macSshdConfig+".claude-bak-"+ts, macSshdConfig)
			return err
		}
	}
	ui.Log("sshd config validation passed")
	if err := run("sudo", "launchctl", "kickstart", "-k", "system/com.openssh.sshd"); err != nil {
		ui.Warn("launchctl kickstart failed (safe to ignore: macOS uses the new config on the next connection)")
	}
	ui.Log("sshd reloaded")
	return nil
}

func macSshdCheck() error {
	out, err := runQuiet("sudo", macSshdBin, "-t")
	if len(out) > 0 {
		os.Stderr.Write(out)
	}
	return err
}
