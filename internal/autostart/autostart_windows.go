//go:build windows

// Package autostart enables/disables launching this binary when the user logs
// in. On Windows it's a HKCU\...\Run registry value (no admin needed).
package autostart

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const valueName = "remote-claude"

// Enabled reports whether launch-on-login is set.
func Enabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(valueName)
	return err == nil
}

// SetEnabled turns launch-on-login on (points at this exe) or off.
func SetEnabled(on bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		_ = k.DeleteValue(valueName) // ignore "not found"
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue(valueName, `"`+exe+`"`)
}
