package core

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/papasaidfine/remote-claude/internal/bridge"
	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/provision"
	"github.com/papasaidfine/remote-claude/internal/store"
)

type fakeManager struct {
	started []bridge.Spec
	stopped []string
}

func (f *fakeManager) Start(s bridge.Spec) error {
	f.started = append(f.started, s)
	return nil
}
func (f *fakeManager) Stop(id string) { f.stopped = append(f.stopped, id) }
func (f *fakeManager) Status(id string) bridge.Status {
	return bridge.Status{HostID: id, State: bridge.StateStopped}
}

type fakeProv struct {
	err   error
	calls int
}

func (f *fakeProv) EnsureClient(h store.Host, alias string) error {
	f.calls++
	return f.err
}
func (f *fakeProv) ServerBootstrap(h store.Host, alias, password string) (provision.ServerResult, error) {
	return provision.ServerResult{}, f.err
}
func (f *fakeProv) EnsureLocalSSHD(disablePassword bool) error { return f.err }

type fakePlat struct{}

func (fakePlat) Name() string            { return "TestOS" }
func (fakePlat) SupportsXray() bool      { return true }
func (fakePlat) StatusIncomingSSH() bool { return false }

func newTestApp(t *testing.T, cfg *store.Config, prov Provisioner) (*App, *fakeManager) {
	t.Helper()
	dir := t.TempDir()
	p := paths.Paths{RCConfigDir: dir, VlessNodes: filepath.Join(dir, "vless-nodes.txt")}
	fm := &fakeManager{}
	return New(cfg, filepath.Join(dir, "config.json"), p, fm, prov, fakePlat{}), fm
}

func TestErrorKinds(t *testing.T) {
	app, _ := newTestApp(t, &store.Config{}, nil)

	if _, err := app.SetAlias("!!!"); KindOf(err) != ErrInvalid {
		t.Errorf("SetAlias garbage: kind = %v, want ErrInvalid", KindOf(err))
	}
	if _, err := app.StartTunnel("nope"); KindOf(err) != ErrNotFound {
		t.Errorf("StartTunnel unknown: kind = %v, want ErrNotFound", KindOf(err))
	}
	if _, err := app.UpdateHost(store.Host{ID: "nope"}); KindOf(err) != ErrNotFound {
		t.Errorf("UpdateHost unknown: kind = %v, want ErrNotFound", KindOf(err))
	}
	if _, err := app.SetupServer("nope", ""); KindOf(err) != ErrUnavailable {
		t.Errorf("SetupServer nil prov: kind = %v, want ErrUnavailable", KindOf(err))
	}
	if _, err := app.AddHost(store.Host{Name: "x"}); KindOf(err) != ErrInvalid {
		t.Errorf("AddHost invalid: kind = %v, want ErrInvalid", KindOf(err))
	}
	if KindOf(errors.New("plain")) != ErrInternal {
		t.Errorf("plain error should classify as ErrInternal")
	}
}

func TestSetupServerRemoteFailure(t *testing.T) {
	cfg := &store.Config{}
	app, _ := newTestApp(t, cfg, &fakeProv{err: fmt.Errorf("boom")})
	h, err := app.AddHost(store.Host{Name: "s", HostName: "h", User: "u"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.SetupServer(h.ID, ""); KindOf(err) != ErrRemote {
		t.Errorf("bootstrap failure: kind = %v, want ErrRemote", KindOf(err))
	}
}

func TestAutoStart(t *testing.T) {
	cfg := &store.Config{
		ClientAlias: "lisa",
		Hosts: []store.Host{
			{ID: "a", Name: "on", HostName: "h", User: "u", AutoStart: true, ReversePort: 2222},
			{ID: "b", Name: "off", HostName: "h", User: "u", ReversePort: 2223},
		},
	}
	prov := &fakeProv{}
	app, fm := newTestApp(t, cfg, prov)

	var warned []string
	app.AutoStart(func(h store.Host, err error) { warned = append(warned, h.Name) })

	if len(warned) != 0 {
		t.Errorf("unexpected warnings: %v", warned)
	}
	if prov.calls != 1 {
		t.Errorf("EnsureClient calls = %d, want 1 (auto-start hosts only)", prov.calls)
	}
	if len(fm.started) != 1 || fm.started[0].HostID != "a" {
		t.Errorf("started = %+v, want just host a", fm.started)
	}
}

func TestAutoStartProvisionFailureWarnsAndSkips(t *testing.T) {
	cfg := &store.Config{
		Hosts: []store.Host{{ID: "a", Name: "on", HostName: "h", User: "u", AutoStart: true, ReversePort: 2222}},
	}
	app, fm := newTestApp(t, cfg, &fakeProv{err: fmt.Errorf("no key")})

	var warned []string
	app.AutoStart(func(h store.Host, err error) { warned = append(warned, h.Name+": "+err.Error()) })

	if len(warned) != 1 {
		t.Fatalf("warnings = %v, want 1", warned)
	}
	if len(fm.started) != 0 {
		t.Errorf("tunnel started despite provision failure: %v", fm.started)
	}
}

func TestNodesRoundtrip(t *testing.T) {
	app, _ := newTestApp(t, &store.Config{}, nil)

	if _, err := app.SetNodes("not-a-vless-url"); KindOf(err) != ErrInvalid {
		t.Fatalf("bad node line: kind = %v, want ErrInvalid", KindOf(err))
	}
	count, err := app.SetNodes("# comment only\n")
	if err != nil || count != 0 {
		t.Fatalf("comments-only: count=%d err=%v", count, err)
	}
	raw, count := app.Nodes()
	if raw != "# comment only\n" || count != 0 {
		t.Errorf("roundtrip: raw=%q count=%d", raw, count)
	}
}
