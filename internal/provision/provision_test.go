package provision

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/platform"
)

func testPaths(t *testing.T) paths.Paths {
	t.Helper()
	dir := t.TempDir()
	sshDir := filepath.Join(dir, ".ssh")
	rc := filepath.Join(dir, "rc")
	return paths.Paths{
		SSHDir:      sshDir,
		KeyPath:     filepath.Join(sshDir, paths.KeyName),
		SSHConfig:   filepath.Join(sshDir, "config"),
		AuthKeys:    filepath.Join(sshDir, "authorized_keys"),
		RCConfigDir: rc,
		VlessNodes:  filepath.Join(rc, "vless-nodes.txt"),
	}
}

func TestEnsureKeyGenerates(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available")
	}
	p := testPaths(t)
	c := New(p, platform.New())
	if err := c.EnsureKey(); err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}
	if _, err := os.Stat(p.KeyPath); err != nil {
		t.Errorf("ssh key not generated: %v", err)
	}
}
