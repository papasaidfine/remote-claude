//go:build darwin

package autostart

import (
	"os"
	"path/filepath"
)

func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", "com.remote-claude.gui.plist")
}

// Enabled reports whether a LaunchAgent for this app exists.
func Enabled() bool {
	_, err := os.Stat(plistPath())
	return err == nil
}

// SetEnabled writes (or removes) a RunAtLoad LaunchAgent for this exe.
func SetEnabled(on bool) error {
	p := plistPath()
	if !on {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>com.remote-claude.gui</string>
  <key>ProgramArguments</key><array><string>` + exe + `</string></array>
  <key>RunAtLoad</key><true/>
</dict></plist>
`
	return os.WriteFile(p, []byte(plist), 0o644)
}
