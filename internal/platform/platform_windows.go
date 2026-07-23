//go:build windows

package platform

import (
	"fmt"
	"os"
	"os/user"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/papasaidfine/remote-claude/internal/sshdconf"
	"github.com/papasaidfine/remote-claude/internal/ui"
)

func winSshdConfig() string {
	pd := os.Getenv("ProgramData")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	return pd + `\ssh\sshd_config`
}

func winSshdExe() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return root + `\System32\OpenSSH\sshd.exe`
}

type windowsPlatform struct{}

func newPlatform() Platform { return windowsPlatform{} }

func (windowsPlatform) Name() string       { return "Windows" }
func (windowsPlatform) SupportsXray() bool { return true }

func isAdmin() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

func (windowsPlatform) RequireElevation() error {
	if isAdmin() {
		return nil
	}
	self, _ := os.Executable()
	return fmt.Errorf("administrator privileges are required for this item (OpenSSH Server install / sshd_config / service control).\n"+
		"    Re-run elevated, e.g.: Start-Process -Verb RunAs %q", self)
}

// SetStrictPerms mirrors the icacls-based ACL of the PowerShell script: only
// SYSTEM, Administrators and the current user, by SID (works on non-English
// Windows). OpenSSH rejects keys/config with loose ACLs.
func (windowsPlatform) SetStrictPerms(path string, isDir bool) error {
	perm := "(F)"
	if isDir {
		perm = "(OI)(CI)(F)"
	}
	sid := "S-1-5-32-544" // Administrators, fallback
	if u, err := user.Current(); err == nil && u.Uid != "" {
		sid = u.Uid
	}
	runQuiet("icacls", path, "/inheritance:r")
	runQuiet("icacls", path, "/grant", "*S-1-5-18:"+perm)     // SYSTEM
	runQuiet("icacls", path, "/grant", "*S-1-5-32-544:"+perm) // Administrators
	runQuiet("icacls", path, "/grant", "*"+sid+":"+perm)      // current user
	return nil
}

func (windowsPlatform) StatusIncomingSSH() bool {
	running, _ := serviceRunning("sshd")
	return running
}

func (windowsPlatform) EnsureIncomingSSH(disablePassword bool) error {
	if err := (windowsPlatform{}).RequireElevation(); err != nil {
		return err
	}
	ui.Log("Checking whether OpenSSH Server is installed")
	if err := ensureCapability("OpenSSH.Server"); err != nil {
		return err
	}
	_ = ensureCapability("OpenSSH.Client") // best-effort (provides ssh/ssh-keygen)

	ui.Log("Starting the sshd service and enabling auto-start")
	if err := enableAndStart("sshd"); err != nil {
		return err
	}

	cfgPath := winSshdConfig()
	if _, err := os.Stat(cfgPath); err != nil {
		return fmt.Errorf("%s not found (it should be generated on the first sshd start)", cfgPath)
	}
	ts := timestamp()
	backup := cfgPath + ".claude-bak-" + ts
	if err := copyFile(cfgPath, backup); err != nil {
		return err
	}
	ui.Log("Backed up sshd_config -> %s", backup)

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return err
	}
	text := string(raw)
	// Comment out the administrators_authorized_keys Match block so admin users
	// also use their own %USERPROFILE%\.ssh\authorized_keys.
	text = sshdconf.CommentOut(text, `(?m)^[ \t]*Match[ \t]+Group[ \t]+administrators[ \t]*\r?$`, "claude-bootstrap disabled")
	text = sshdconf.CommentOut(text, `(?m)^[ \t]*AuthorizedKeysFile[ \t]+__PROGRAMDATA__[/\\]ssh[/\\]administrators_authorized_keys[ \t]*\r?$`, "claude-bootstrap disabled")
	text = sshdconf.SetDirective(text, "PubkeyAuthentication", "yes")
	text = sshdconf.SetDirective(text, "AuthorizedKeysFile", ".ssh/authorized_keys")
	if disablePassword {
		text = sshdconf.SetDirective(text, "PasswordAuthentication", "no")
	}
	if err := os.WriteFile(cfgPath, []byte(text), 0o644); err != nil {
		return err
	}

	ui.Log("Validating the sshd config (sshd -t)")
	if out, err := runQuiet(winSshdExe(), "-t"); err != nil {
		if len(out) > 0 {
			os.Stderr.Write(out)
		}
		_ = copyFile(backup, cfgPath)
		return fmt.Errorf("sshd config validation failed; the backup was restored")
	}
	ui.Log("Config validation passed, restarting sshd")
	return restartService("sshd")
}

// ensureCapability installs a Windows optional feature (OpenSSH.*) if missing,
// via PowerShell (there is no clean Go API for Features on Demand).
func ensureCapability(prefix string) error {
	script := fmt.Sprintf(`$ErrorActionPreference='Stop'
$cap = Get-WindowsCapability -Online | Where-Object Name -like '%s*' | Select-Object -First 1
if (-not $cap) { Write-Error '%s capability not found; Windows 10 1809+ / 11 required.'; exit 1 }
if ($cap.State -ne 'Installed') { Add-WindowsCapability -Online -Name $cap.Name | Out-Null }`, prefix, prefix)
	out, err := runQuiet("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	if err != nil {
		if len(out) > 0 {
			os.Stderr.Write(out)
		}
		return fmt.Errorf("installing %s failed", prefix)
	}
	return nil
}

// serviceRunning queries a service's state with only SC_MANAGER_CONNECT +
// SERVICE_QUERY_STATUS, which any (non-admin) user can do — unlike mgr.Connect,
// which asks for SC_MANAGER_ALL_ACCESS and fails without elevation.
func serviceRunning(name string) (bool, error) {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return false, err
	}
	defer windows.CloseServiceHandle(scm)
	np, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return false, err
	}
	h, err := windows.OpenService(scm, np, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return false, err
	}
	defer windows.CloseServiceHandle(h)
	var status windows.SERVICE_STATUS
	if err := windows.QueryServiceStatus(h, &status); err != nil {
		return false, err
	}
	return status.CurrentState == windows.SERVICE_RUNNING, nil
}

func enableAndStart(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return err
	}
	defer s.Close()
	if cfg, err := s.Config(); err == nil {
		cfg.StartType = mgr.StartAutomatic
		_ = s.UpdateConfig(cfg)
	}
	if q, err := s.Query(); err == nil && q.State == svc.Running {
		return nil
	}
	if err := s.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", name, err)
	}
	return nil
}

func restartService(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return err
	}
	defer s.Close()
	if q, _ := s.Query(); q.State == svc.Running {
		if _, err := s.Control(svc.Stop); err == nil {
			for i := 0; i < 50; i++ {
				if q, _ := s.Query(); q.State == svc.Stopped {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
	}
	return s.Start()
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}
