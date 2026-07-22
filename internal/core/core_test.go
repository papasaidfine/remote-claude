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
	started []string
	stopped []string
}

func (f *fakeMgr) Start(s bridge.Spec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, s.Alias)
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

func TestAddHostWritesConfigAndShows(t *testing.T) {
	app, _, _ := newTestApp(t)
	if _, err := app.SetAlias("lisa-laptop"); err != nil {
		t.Fatalf("SetAlias: %v", err)
	}
	if err := app.AddHost("workbox", "srv.example.com", "dev", 2200); err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	cfg := readSSH(t, app)
	for _, want := range []string{"Host workbox", "HostName srv.example.com", "User dev", "Port 2200", "SetEnv LC_CLIENT_NAME=lisa-laptop"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config missing %q:\n%s", want, cfg)
		}
	}
	st := app.State()
	if len(st.Hosts) != 1 || st.Hosts[0].Alias != "workbox" {
		t.Fatalf("host not shown in state: %+v", st.Hosts)
	}
}

func TestAddHostRejectsDuplicateAndBadAlias(t *testing.T) {
	app, _, _ := newTestApp(t)
	app.AddHost("wb", "h", "u", 22)
	if err := app.AddHost("wb", "h2", "u", 22); KindOf(err) != ErrInvalid {
		t.Errorf("duplicate host should be ErrInvalid, got %v", err)
	}
	if err := app.AddHost("has space", "h", "u", 22); KindOf(err) != ErrInvalid {
		t.Errorf("alias with space should be ErrInvalid, got %v", err)
	}
}

func TestReverseTunnelAndProxyAreConfig(t *testing.T) {
	app, _, _ := newTestApp(t)
	app.AddHost("wb", "h", "u", 22)
	if err := app.SetReverseTunnel("wb", 2222); err != nil {
		t.Fatalf("SetReverseTunnel: %v", err)
	}
	if err := app.SetProxy("wb", true); err != nil {
		t.Fatalf("SetProxy: %v", err)
	}
	h := app.State().Hosts[0]
	if !h.HasReverse || h.ReversePort != "2222" {
		t.Errorf("reverse tunnel not in config summary: %+v", h.Summary)
	}
	if !h.HasProxy {
		t.Errorf("proxy not in config summary: %+v", h.Summary)
	}
	app.SetReverseTunnel("wb", 0)
	if app.State().Hosts[0].HasReverse {
		t.Error("reverse tunnel not cleared")
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
	if !strings.Contains(cfg, "# my note") || !strings.Contains(cfg, "LocalForward 9000 localhost:9000") {
		t.Errorf("unknown lines lost:\n%s", cfg)
	}
	if !strings.Contains(cfg, "User bob") {
		t.Errorf("param not written:\n%s", cfg)
	}
}

func TestStartTunnelUnknownHost(t *testing.T) {
	app, _, _ := newTestApp(t)
	if _, err := app.StartTunnel("nope"); KindOf(err) != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestStartTunnelEnsuresKeyAndStarts(t *testing.T) {
	app, fm, fp := newTestApp(t)
	app.AddHost("wb", "h", "u", 22)
	if _, err := app.StartTunnel("wb"); err != nil {
		t.Fatalf("StartTunnel: %v", err)
	}
	if fp.keyCalls != 1 {
		t.Errorf("EnsureKey calls = %d", fp.keyCalls)
	}
	if len(fm.started) != 1 || fm.started[0] != "wb" {
		t.Errorf("bridge not started for alias: %v", fm.started)
	}
}

func TestRemoveHostStopsAndDeletes(t *testing.T) {
	app, fm, _ := newTestApp(t)
	app.AddHost("wb", "h", "u", 22)
	if err := app.RemoveHost("wb"); err != nil {
		t.Fatalf("RemoveHost: %v", err)
	}
	if len(fm.stopped) != 1 || fm.stopped[0] != "wb" {
		t.Errorf("tunnel not stopped: %v", fm.stopped)
	}
	if len(app.State().Hosts) != 0 {
		t.Error("host not removed from config")
	}
}

func TestSetupServerUsesConfigReversePort(t *testing.T) {
	app, _, fp := newTestApp(t)
	app.SetAlias("lisa")
	app.AddHost("wb", "h", "u", 22)
	app.SetReverseTunnel("wb", 2223)
	if _, err := app.SetupServer("wb", ""); err != nil {
		t.Fatalf("SetupServer: %v", err)
	}
	if fp.bootCalls != 1 || fp.lastAlias != "wb" || fp.lastPort != 2223 {
		t.Errorf("bootstrap not called with alias/port: %+v", fp)
	}
}

func TestSetupServerUnavailableWhenNoProv(t *testing.T) {
	dir := t.TempDir()
	ssh := filepath.Join(dir, ".ssh")
	p := paths.Paths{SSHDir: ssh, SSHConfig: filepath.Join(ssh, "config"), RCConfigDir: filepath.Join(dir, "rc"), VlessNodes: filepath.Join(dir, "rc", "n.txt")}
	app := New(&store.Config{}, filepath.Join(dir, "c.json"), p, &fakeMgr{}, nil, fakePlat{})
	if _, err := app.SetupServer("x", ""); KindOf(err) != ErrUnavailable {
		t.Errorf("want ErrUnavailable, got %v", err)
	}
}

func TestAutoStartStartsFlaggedHosts(t *testing.T) {
	app, fm, _ := newTestApp(t)
	app.AddHost("wb", "h", "u", 22)
	app.SetAutoStart("wb", true)
	app.AutoStart(func(string, error) {})
	if len(fm.started) != 1 || fm.started[0] != "wb" {
		t.Errorf("auto-start did not start the host: %v", fm.started)
	}
}

func TestNodesRoundtrip(t *testing.T) {
	app, _, _ := newTestApp(t)
	if _, err := app.SetNodes("not-a-vless-url"); KindOf(err) != ErrInvalid {
		t.Fatalf("bad node line: kind = %v, want ErrInvalid", KindOf(err))
	}
	count, err := app.SetNodes("# comment only\n")
	if err != nil || count != 0 {
		t.Fatalf("comments-only: count=%d err=%v", count, err)
	}
	raw, _ := app.Nodes()
	if raw != "# comment only\n" {
		t.Errorf("roundtrip raw=%q", raw)
	}
}
