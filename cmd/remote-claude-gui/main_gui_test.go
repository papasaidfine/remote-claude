//go:build gui

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

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

// newTestGUI builds a gui over a real core.App backed by a temp ssh config,
// using Fyne's headless test driver. aliases each become a tunnel host.
func newTestGUI(t *testing.T, aliases ...string) (*gui, *fakeMgr) {
	t.Helper()
	a := test.NewTempApp(t)

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

	w := a.NewWindow("test")
	g := &gui{core: appCore, app: a, win: w, lang: i18n.EN, pr: i18n.P(i18n.EN)}
	w.SetContent(g.build())
	return g, mgr
}

// labelTexts collects every label caption under obj, depth first.
func labelTexts(obj fyne.CanvasObject) []string {
	var out []string
	switch o := obj.(type) {
	case *widget.Label:
		out = append(out, o.Text)
	case *fyne.Container:
		for _, child := range o.Objects {
			out = append(out, labelTexts(child)...)
		}
	}
	return out
}

func joinLabels(obj fyne.CanvasObject) string {
	return strings.Join(labelTexts(obj), " | ")
}

// A tick that rebuilds the host list strands the previous widget tree in Fyne's
// renderer cache, which is only swept when a window actually repaints. Over a
// tray-hidden session that grew to gigabytes, so cards must be reused.
func TestRefreshReusesHostCards(t *testing.T) {
	g, _ := newTestGUI(t, "alpha", "beta")

	g.refresh()
	first := append([]fyne.CanvasObject(nil), g.hostsBox.Objects...)
	if len(first) != 2 {
		t.Fatalf("want 2 host cards, got %d", len(first))
	}

	for i := 0; i < 5; i++ {
		g.refresh()
	}

	if got := len(g.hostsBox.Objects); got != len(first) {
		t.Fatalf("card count changed across refreshes: %d -> %d", len(first), got)
	}
	for i, want := range first {
		if g.hostsBox.Objects[i] != want {
			t.Errorf("card %d was rebuilt; refresh must update cards in place", i)
		}
	}
}

// Reusing cards is only correct if they still track live state.
func TestRefreshUpdatesTunnelStateInPlace(t *testing.T) {
	g, mgr := newTestGUI(t, "alpha")

	g.refresh()
	card := g.hostsBox.Objects[0]
	if got := joinLabels(card); !strings.Contains(got, g.stateName(bridge.StateStopped)) {
		t.Fatalf("card does not show the stopped state: %s", got)
	}

	mgr.state = bridge.StateUp
	g.refresh()

	if g.hostsBox.Objects[0] != card {
		t.Fatalf("card was rebuilt on a status change")
	}
	if got := joinLabels(card); !strings.Contains(got, g.stateName(bridge.StateUp)) {
		t.Errorf("card did not pick up the new state, shows: %s", got)
	}
}

// While the window is hidden in the tray nothing repaints, so Fyne never sweeps
// its renderer cache and anything a refresh touches is retained for good. The
// tick has to stand down until the window is back on screen.
func TestTickSkipsRefreshWhileHidden(t *testing.T) {
	g, mgr := newTestGUI(t, "alpha")
	g.shown.Store(true)
	g.tick()
	card := g.hostsBox.Objects[0]
	before := joinLabels(card)

	g.shown.Store(false)
	mgr.state = bridge.StateUp
	g.tick()
	if got := joinLabels(card); got != before {
		t.Errorf("tick refreshed a hidden window:\n before: %s\n after:  %s", before, got)
	}

	g.shown.Store(true)
	g.tick()
	if got := joinLabels(card); !strings.Contains(got, g.stateName(bridge.StateUp)) {
		t.Errorf("tick did not resume once the window was shown again: %s", got)
	}
}

func TestRefreshTracksHostsAppearingAndDisappearing(t *testing.T) {
	g, _ := newTestGUI(t, "alpha")

	g.refresh()
	if got := len(g.hostsBox.Objects); got != 1 {
		t.Fatalf("want 1 card, got %d", got)
	}
	alpha := g.hostsBox.Objects[0]

	if err := g.core.AddHost("beta", "10.0.0.2", "dev", 22); err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	g.refresh()
	if got := len(g.hostsBox.Objects); got != 2 {
		t.Fatalf("want 2 cards after adding a host, got %d", got)
	}
	if g.hostsBox.Objects[0] != alpha {
		t.Errorf("the untouched host's card was rebuilt when another host appeared")
	}

	if err := g.core.RemoveHost("beta"); err != nil {
		t.Fatalf("RemoveHost: %v", err)
	}
	g.refresh()
	if got := len(g.hostsBox.Objects); got != 1 {
		t.Fatalf("want 1 card after removing a host, got %d", got)
	}
}
