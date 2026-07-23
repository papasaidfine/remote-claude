//go:build linux

package autostart

import (
	"os"
	"path/filepath"
)

func desktopPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "autostart", "remote-claude.desktop")
}

// Enabled reports whether an autostart .desktop entry exists.
func Enabled() bool {
	_, err := os.Stat(desktopPath())
	return err == nil
}

// SetEnabled writes (or removes) an XDG autostart .desktop entry for this exe.
func SetEnabled(on bool) error {
	p := desktopPath()
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
	entry := "[Desktop Entry]\nType=Application\nName=remote-claude\nExec=" + exe +
		"\nTerminal=false\nX-GNOME-Autostart-enabled=true\n"
	return os.WriteFile(p, []byte(entry), 0o644)
}
