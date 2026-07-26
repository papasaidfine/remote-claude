package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/papasaidfine/remote-claude/internal/bridge"
	"github.com/papasaidfine/remote-claude/internal/core"
	"github.com/papasaidfine/remote-claude/internal/i18n"
	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/provision"
	"github.com/papasaidfine/remote-claude/internal/store"
)

type fakeMgr struct{ state bridge.State }

func (f *fakeMgr) Start(bridge.Spec) error { return nil }
func (f *fakeMgr) Stop(string)             {}
func (f *fakeMgr) Status(alias string) bridge.Status {
	return bridge.Status{Alias: alias, State: f.state}
}

type fakeProv struct{}

func (fakeProv) EnsureKey() error { return nil }
func (fakeProv) ServerBootstrap(string, string, int) (provision.ServerResult, error) {
	return provision.ServerResult{}, nil
}
func (fakeProv) PublicKey() (string, error) { return "ssh-ed25519 AAAATEST", nil }
func (fakeProv) EnsureLocalSSHD(bool) error { return nil }

type fakePlat struct{}

func (fakePlat) Name() string            { return "TestOS" }
func (fakePlat) SupportsXray() bool      { return true }
func (fakePlat) StatusIncomingSSH() bool { return false }
func (fakePlat) OpenURL(string) error    { return nil }

// newTestUI builds a UI over a real core.App backed by a temp ssh config, wired
// to a simulation screen so tests can read back what was actually rendered.
func newTestUI(t *testing.T, aliases ...string) (*UI, *fakeMgr, tcell.SimulationScreen) {
	t.Helper()
	dir := t.TempDir()
	ssh := filepath.Join(dir, ".ssh")
	p := paths.Paths{
		SSHDir:      ssh,
		SSHConfig:   filepath.Join(ssh, "config"),
		RCConfigDir: filepath.Join(dir, "rc"),
		VlessNodes:  filepath.Join(dir, "rc", "vless-nodes.txt"),
	}
	mgr := &fakeMgr{state: bridge.StateStopped}
	appCore := core.New(&store.Config{}, filepath.Join(dir, "config.json"), p, mgr, fakeProv{}, fakePlat{})
	for i, alias := range aliases {
		if err := appCore.AddHost(alias, "10.0.0.1", "dev", 22); err != nil {
			t.Fatalf("AddHost(%q): %v", alias, err)
		}
		if err := appCore.SetReverseTunnel(alias, 2222+i); err != nil {
			t.Fatalf("SetReverseTunnel(%q): %v", alias, err)
		}
	}

	u := New(appCore, i18n.EN, "v9.9.9")
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("simulation screen: %v", err)
	}
	u.app.SetScreen(screen)

	// tview's draw path goes through the update queue, and that queue is only
	// drained by a running event loop — so the loop has to be up for anything to
	// render at all.
	go func() { _ = u.app.Run() }()

	// Resize after the loop is up: tview reads the screen size when it starts and
	// then only on resize events, so sizing beforehand is silently ignored and
	// everything renders into the simulation screen's 80x25 default.
	onLoop(u, func() {})
	screen.SetSize(110, 40)
	onLoop(u, func() {})
	t.Cleanup(u.app.Stop) // Stop finalises the screen itself; doing it again panics
	return u, mgr, screen
}

// onLoop runs fn on the draw goroutine and waits for it to finish.
func onLoop(u *UI, fn func()) {
	done := make(chan struct{})
	u.app.QueueUpdateDraw(fn)
	u.app.QueueUpdate(func() { close(done) })
	<-done
}

// render refreshes and draws the UI, then returns the screen contents as text.
func render(t *testing.T, u *UI, screen tcell.SimulationScreen) string {
	t.Helper()
	onLoop(u, u.refresh)
	cells, w, h := screen.GetContents()
	var b strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			runes := cells[y*w+x].Runes
			if len(runes) == 0 {
				b.WriteByte(' ')
				continue
			}
			b.WriteRune(runes[0])
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func TestHostsPageListsEveryHost(t *testing.T) {
	u, _, screen := newTestUI(t, "alpha", "beta")

	got := render(t, u, screen)

	for _, want := range []string{"alpha", "beta", "dev@10.0.0.1:22"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered screen is missing %q:\n%s", want, got)
		}
	}
}

func TestHostsPageShowsLiveTunnelState(t *testing.T) {
	u, mgr, screen := newTestUI(t, "alpha")

	if got := render(t, u, screen); !strings.Contains(got, i18n.T(i18n.EN, "state_stopped")) {
		t.Fatalf("stopped tunnel not shown:\n%s", got)
	}

	mgr.state = bridge.StateUp
	if got := render(t, u, screen); !strings.Contains(got, i18n.T(i18n.EN, "state_up")) {
		t.Errorf("state change not picked up:\n%s", got)
	}
}

func TestStatusLineReportsThePlatformAndNodeCount(t *testing.T) {
	u, _, screen := newTestUI(t, "alpha")

	got := render(t, u, screen)

	if !strings.Contains(got, "TestOS") {
		t.Errorf("status line is missing the platform:\n%s", got)
	}
	if !strings.Contains(got, "0 proxy node") {
		t.Errorf("status line is missing the node count:\n%s", got)
	}
}

func TestSwitchingToSettingsAndBack(t *testing.T) {
	u, _, screen := newTestUI(t, "alpha")
	render(t, u, screen)

	onLoop(u, func() { u.showPage(pageSettings) })
	got := render(t, u, screen)
	if !strings.Contains(got, i18n.T(i18n.EN, "settings_proxy")) {
		t.Fatalf("settings page did not come up:\n%s", got)
	}

	onLoop(u, func() { u.showPage(pageHosts) })
	got = render(t, u, screen)
	if !strings.Contains(got, "alpha") {
		t.Errorf("hosts page did not come back:\n%s", got)
	}
}

func TestEmptyHostListSaysSo(t *testing.T) {
	u, _, screen := newTestUI(t)

	if got := render(t, u, screen); !strings.Contains(got, i18n.T(i18n.EN, "no_hosts")) {
		t.Errorf("empty host list gives no explanation:\n%s", got)
	}
}

// The selected row is what every host key acts on, so it must survive a refresh
// — the ticker redraws twice a second and would otherwise walk the cursor back
// to the top while the user is reading.
func TestRefreshKeepsTheSelectedRow(t *testing.T) {
	u, _, screen := newTestUI(t, "alpha", "beta", "gamma")
	render(t, u, screen)

	var before, after *core.HostView
	onLoop(u, func() {
		u.table.Select(3, 0) // third host (row 0 is the header)
		before = u.selectedHost()
	})
	if before == nil || before.Alias != "gamma" {
		t.Fatalf("selectedHost() = %v, want gamma", before)
	}

	render(t, u, screen)

	onLoop(u, func() { after = u.selectedHost() })
	if after == nil || after.Alias != "gamma" {
		t.Errorf("selection moved across a refresh: %v", after)
	}
}

func TestSelectedHostIsNilWhenThereAreNoHosts(t *testing.T) {
	u, _, screen := newTestUI(t)
	render(t, u, screen)

	var h *core.HostView
	onLoop(u, func() { h = u.selectedHost() })
	if h != nil {
		t.Errorf("selectedHost() = %v with no hosts, want nil", h)
	}
}
