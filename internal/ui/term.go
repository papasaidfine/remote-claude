package ui

import "os"

// isTerminal reports whether f is attached to a character device (a TTY),
// used to decide whether ANSI colors are safe to emit.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
