package webui

import (
	"os"
	"strings"

	"github.com/papasaidfine/remote-claude/internal/paths"
)

func readFileString(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}

// writeNodesFile persists the vless nodes file (raw text; one vless:// per line).
func writeNodesFile(p paths.Paths, raw string) error {
	if err := os.MkdirAll(p.RCConfigDir, 0o755); err != nil {
		return err
	}
	if raw != "" && !strings.HasSuffix(raw, "\n") {
		raw += "\n"
	}
	return os.WriteFile(p.VlessNodes, []byte(raw), 0o600)
}
