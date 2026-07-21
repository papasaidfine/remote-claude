package provision

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/platform"
	"github.com/papasaidfine/remote-claude/internal/sshconfig"
	"github.com/papasaidfine/remote-claude/internal/store"
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

func TestEnsureClientWritesBlock(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available")
	}
	p := testPaths(t)
	c := New(p, platform.New())
	h := store.Host{Name: "workbox", HostName: "srv.example.com", User: "dev", Port: 22, ReversePort: 2222}
	if err := c.EnsureClient(h, "lisa-laptop"); err != nil {
		t.Fatalf("EnsureClient: %v", err)
	}
	raw, err := os.ReadFile(p.SSHConfig)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	cfg := string(raw)
	if sshconfig.Value(cfg, "HostName") != "srv.example.com" {
		t.Errorf("HostName not written: %q", sshconfig.Value(cfg, "HostName"))
	}
	if !strings.Contains(cfg, "SetEnv LC_CLIENT_NAME=lisa-laptop") {
		t.Errorf("missing SetEnv:\n%s", cfg)
	}
	if strings.Contains(cfg, "RemoteForward") {
		t.Errorf("client block must not carry RemoteForward:\n%s", cfg)
	}
	if strings.Contains(cfg, "ProxyCommand") {
		t.Errorf("xray off but ProxyCommand present:\n%s", cfg)
	}
	// the key should have been created
	if _, err := os.Stat(p.KeyPath); err != nil {
		t.Errorf("ssh key not generated: %v", err)
	}
}

func TestEnsureClientXrayWithoutBinaryFails(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available")
	}
	p := testPaths(t)
	c := New(p, platform.New())
	h := store.Host{Name: "wb", HostName: "h", User: "u", Port: 22, ReversePort: 2222, UseXray: true}
	err := c.EnsureClient(h, "me")
	if err == nil {
		t.Fatal("expected error when xray enabled but binary missing")
	}
}
