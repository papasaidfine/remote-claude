// Package fsutil holds small filesystem helpers shared across the tool.
package fsutil

import (
	"io"
	"os"
	"time"
)

// Backup copies path to "path.claude-bak-<timestamp>" and returns the backup
// path. It is a no-op (empty string, nil) when path does not exist.
func Backup(path string) (string, error) {
	src, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer src.Close()
	dst := path + ".claude-bak-" + time.Now().Format("20060102-150405")
	out, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer out.Close()
	if _, err := io.Copy(out, src); err != nil {
		return "", err
	}
	return dst, nil
}
