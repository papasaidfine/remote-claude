//go:build !windows && !darwin && !linux

package autostart

import "errors"

// Enabled reports whether launch-on-login is set (never, on unsupported OSes).
func Enabled() bool { return false }

// SetEnabled is unsupported on this OS.
func SetEnabled(on bool) error {
	return errors.New("start-on-login is not supported on this OS")
}
