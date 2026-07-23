package core

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/papasaidfine/remote-claude/internal/bridge"
	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/provision"
	"github.com/papasaidfine/remote-claude/internal/store"
)

type fakeMgr struct {
	mu      sync.Mutex
	started []bridge.Spec
	stopped []string
}

func (f *fakeMgr) Start(s bridge.Spec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, s)
	return nil
}
func (f *fakeMgr) Stop(a string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, a)
}
func (f *fakeMgr) Status(a string) bridge.Status {
	return bridge.Status{Alias: a, State: bridge.StateStopped}
}

type fakeProv struct {
	keyCalls  int
	bootCalls int
	lastAlias string
	lastPort  int
}

func (f *fakeProv) EnsureKey() error { f.keyCalls++; return nil }
func (f *fakeProv) ServerBootstrap(alias, clientAlias string, port int, pw string) (provision.ServerResult, error) {
	f.bootCalls++
	f.lastAlias = alias
	f.lastPort = port
	return provision.ServerResult{Alias: clientAlias, Authorized: true, ServerPubKey: "k"}, nil
}
func (f *fakeProv) EnsureLocalSSHD(bool) error { return nil }

type fakePlat struct{}

func (fakePlat) Name() string            { return "TestOS" }
func (fakePlat) SupportsXray() bool      { return true }
func (fakePlat) StatusIncomingSSH() bool { return false }

func newTestApp(t *testing.T) (*App, *fakeMgr, *fakeProv) {
	t.Helper()
	dir := t.TempDir()
	ssh := filepath.Join(dir, ".ssh")
	p := paths.Paths{
		SSHDir:      ssh,
		SSHConfig:   filepath.Join(ssh, "config"),
		RCConfigDir: filepath.Join(dir, "rc"),
		VlessNodes:  filepath.Join(dir, "rc", "vless-nodes.txt"),
	}
	fm, fp := &fakeMgr{}, &fakeProv{}
	app := New(&store.Config{}, filepath.Join(dir, "config.json"), p, fm, fp, fakePlat{})
	return app, fm, fp
}

func readSSH(t *testing.T, a *App) string {
	t.Helper()
	b, _ := os.ReadFile(a.sshPath)
	return string(b)
}

func TestAddHostWritesIdentityLessBlock(t *testing.T) {
	app, _, _ := newTestApp(t)
	app.SetAlias("lc-pc")
	if err := app.AddHost("workbox", "srv.example.com", "dev", 2200); err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	cfg := readSSH(t, app)
	for _, want := range []string{"Host workbox", "HostName srv.example.com", "User dev", "Port 2200", "SetEnv LC_CLIENT_NAME=lc-pc"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config missing %q:\n%s", want, cfg)
		}
	}
	for _, notWant := range []string{"IdentityFile", "IdentitiesOnly", "RemoteForward"} {
		if strings.Contains(cfg, notWant) {
			t.Errorf("config should not contain %q:\n%s", notWant, cfg)
		}
	}
	if st := app.State(); len(st.Hosts) != 1 || st.Hosts[0].Alias != "workbox" {
		t.Fatalf("host not shown: %+v", st.Hosts)
	}
}

func TestReverseTunnelIsMetadataNotConfig(t *testing.T) {
	app, _, _ := newTestApp(t)
	app.AddHost("wb", "h", "u", 22)
	if err := app.SetReverseTunnel("wb", 2222); err != nil {
		t.Fatalf("SetReverseTunnel: %v", err)
	}
	if strings.Contains(readSSH(t, app), "RemoteForward") {
		t.Errorf("reverse tunnel must NOT be written to ssh config:\n%s", readSSH(t, app))
	}
	h := app.State().Hosts[0]
	if !h.HasReverse || h.ReversePort != 2222 {
		t.Errorf("reverse tunnel metadata not reflected: %+v", h)
	}
	// proxy IS config
	if err := app.SetProxy("wb", true); err != nil {
		t.Fatalf("SetProxy: %v", err)
	}
	if !strings.Contains(readSSH(t, app), "ProxyCommand") || !app.State().Hosts[0].HasProxy {
		t.Error("proxy should be in ssh config")
	}
	// clearing the port turns it off
	app.SetReverseTunnel("wb", 0)
	if app.State().Hosts[0].HasReverse {
		t.Error("reverse tunnel not cleared")
	}
}

func TestSetReverseTunnelStripsLeftoverManagedKeys(t *testing.T) {
	app, _, _ := newTestApp(t)
	os.MkdirAll(filepath.Dir(app.sshPath), 0o700)
	os.WriteFile(app.sshPath, []byte("Host wb\n    HostName h\n    IdentityFile ~/.ssh/id_ed25519\n"+
		"    IdentitiesOnly yes\n    RemoteForward 127.0.0.1:2222 127.0.0.1:22\n    ServerAliveInterval 30\n    # keep me\n"), 0o600)
	if err := app.SetReverseTunnel("wb", 2223); err != nil {
		t.Fatalf("SetReverseTunnel: %v", err)
	}
	cfg := readSSH(t, app)
	for _, gone := range []string{"IdentityFile", "IdentitiesOnly", "RemoteForward", "ServerAliveInterval"} {
		if strings.Contains(cfg, gone) {
			t.Errorf("managed key %q not stripped:\n%s", gone, cfg)
		}
	}
	if !strings.Contains(cfg, "# keep me") || !strings.Contains(cfg, "HostName h") {
		t.Errorf("non-managed lines lost:\n%s", cfg)
	}
}

func TestSetParamPreservesUnknownLines(t *testing.T) {
	app, _, _ := newTestApp(t)
	os.MkdirAll(filepath.Dir(app.sshPath), 0o700)
	os.WriteFile(app.sshPath, []byte("Host wb\n    HostName h\n    # my note\n    LocalForward 9000 localhost:9000\n"), 0o600)
	if err := app.SetParam("wb", "User", "bob"); err != nil {
		t.Fatalf("SetParam: %v", err)
	}
	cfg := readSSH(t, app)
	if !strings.Contains(cfg, "# my note") || !strings.Contains(cfg, "LocalForward 9000 localhost:9000") || !strings.Contains(cfg, "User bob") {
		t.Errorf("SetParam mangled the block:\n%s", cfg)
	}
}

func TestStartTunnelRequiresReversePort(t *testing.T) {
	app, _, _ := newTestApp(t)
	app.AddHost("wb", "h", "u", 22)
	if _, err := app.StartTunnel("wb"); KindOf(err) != ErrInvalid {
		t.Fatalf("want ErrInvalid without reverse port, got %v", err)
	}
	if _, err := app.StartTunnel("nope"); KindOf(err) != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestStartTunnelEnsuresKeyAndStartsWithReversePort(t *testing.T) {
	app, fm, fp := newTestApp(t)
	app.AddHost("wb", "h", "u", 22)
	app.SetReverseTunnel("wb", 2222)
	if _, err := app.StartTunnel("wb"); err != nil {
		t.Fatalf("StartTunnel: %v", err)
	}
	if fp.keyCalls != 1 {
		t.Errorf("EnsureKey calls = %d", fp.keyCalls)
	}
	if len(fm.started) != 1 || fm.started[0].Alias != "wb" || fm.started[0].ReversePort != 2222 {
		t.Errorf("bridge not started with alias+port: %+v", fm.started)
	}
}

func TestRemoveHostStopsDeletesAndClearsMeta(t *testing.T) {
	app, fm, _ := newTestApp(t)
	app.AddHost("wb", "h", "u", 22)
	app.SetReverseTunnel("wb", 2222)
	if err := app.RemoveHost("wb"); err != nil {
		t.Fatalf("RemoveHost: %v", err)
	}
	if len(fm.stopped) != 1 || fm.stopped[0] != "wb" {
		t.Errorf("tunnel not stopped: %v", fm.stopped)
	}
	if len(app.State().Hosts) != 0 || app.meta.Host("wb").ReversePort != 0 {
		t.Error("host/meta not removed")
	}
}

func TestSetupServerUsesMetadataReversePort(t *testing.T) {
	app, _, fp := newTestApp(t)
	app.SetAlias("lc-pc")
	app.AddHost("wb", "h", "u", 22)
	app.SetReverseTunnel("wb", 2223)
	if _, err := app.SetupServer("wb", ""); err != nil {
		t.Fatalf("SetupServer: %v", err)
	}
	if fp.bootCalls != 1 || fp.lastAlias != "wb" || fp.lastPort != 2223 {
		t.Errorf("bootstrap not called with alias/port: %+v", fp)
	}
}

func TestAutoStartStartsFlaggedHosts(t *testing.T) {
	app, fm, _ := newTestApp(t)
	app.AddHost("wb", "h", "u", 22)
	app.SetReverseTunnel("wb", 2222)
	app.SetAutoStart("wb", true)
	app.AutoStart(func(string, error) {})
	if len(fm.started) != 1 || fm.started[0].Alias != "wb" {
		t.Errorf("auto-start did not start the host: %+v", fm.started)
	}
}

func TestNodesRoundtrip(t *testing.T) {
	app, _, _ := newTestApp(t)
	if _, err := app.SetNodes("not-a-vless-url"); KindOf(err) != ErrInvalid {
		t.Fatalf("bad node line: kind = %v, want ErrInvalid", KindOf(err))
	}
	if _, err := app.SetNodes("# comment only\n"); err != nil {
		t.Fatalf("comments-only: %v", err)
	}
	if raw, _ := app.Nodes(); raw != "# comment only\n" {
		t.Errorf("roundtrip raw=%q", raw)
	}
}
