//go:build gui

// Command remote-claude-gui is the native desktop front-end. It builds the same
// core.App the web UI drives and renders it with Fyne. Build with:
//
//	CGO_ENABLED=1 go build -tags gui ./cmd/remote-claude-gui
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/papasaidfine/remote-claude/internal/bridge"
	"github.com/papasaidfine/remote-claude/internal/core"
	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/platform"
	"github.com/papasaidfine/remote-claude/internal/provision"
	"github.com/papasaidfine/remote-claude/internal/sshbin"
	"github.com/papasaidfine/remote-claude/internal/store"
)

// version is stamped at release time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	p, err := paths.Resolve()
	if err != nil {
		die(err)
	}
	cfg, err := store.Load(store.Path(p))
	if err != nil {
		die(err)
	}
	plat := platform.New()
	mgr := bridge.NewManager(sshbin.SSH())
	prov := provision.New(p, plat)
	appCore := core.New(cfg, store.Path(p), p, mgr, prov, plat)
	appCore.AutoStart(func(string, error) {})

	a := app.New()
	a.SetIcon(theme.ComputerIcon())
	// Quitting stops the tunnels (their ssh children would otherwise be orphaned).
	a.Lifecycle().SetOnStopped(func() { mgr.StopAll() })

	w := a.NewWindow("remote-claude " + version)
	w.Resize(fyne.NewSize(720, 620))

	g := &gui{core: appCore, win: w}
	w.SetContent(g.build())
	g.refresh()
	go g.autoRefresh()

	// System tray: closing the window hides to the tray so the app keeps holding
	// the tunnels up; Fyne adds a native Quit item. Only where a tray exists.
	if desk, ok := a.(desktop.App); ok {
		desk.SetSystemTrayIcon(theme.ComputerIcon())
		desk.SetSystemTrayMenu(fyne.NewMenu("remote-claude",
			fyne.NewMenuItem("Open", func() { w.Show() }),
		))
		w.SetCloseIntercept(func() { w.Hide() })
	}

	w.ShowAndRun()
}

// autoRefresh re-renders live tunnel status on a timer. UI mutations must run on
// the main goroutine, so it hops through fyne.Do.
func (g *gui) autoRefresh() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for range t.C {
		fyne.Do(g.refresh)
	}
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "remote-claude-gui:", err)
	os.Exit(1)
}

type gui struct {
	core     *core.App
	win      fyne.Window
	alias    *widget.Entry
	status   *widget.Label
	hostsBox *fyne.Container
}

func (g *gui) build() fyne.CanvasObject {
	g.alias = widget.NewEntry()
	g.alias.SetPlaceHolder("this machine's name, e.g. lisa-laptop")
	saveAlias := widget.NewButton("Save name", func() {
		if _, err := g.core.SetAlias(g.alias.Text); err != nil {
			dialog.ShowError(err, g.win)
			return
		}
		g.refresh()
	})
	aliasRow := container.NewBorder(nil, nil, widget.NewLabel("Name"), saveAlias, g.alias)

	toolbar := container.NewHBox(
		widget.NewButton("+ Add host", g.showAddHost),
		widget.NewButton("Xray nodes", g.showNodes),
		widget.NewButton("Local ssh server", g.showLocalSSHD),
		widget.NewButton("Refresh", g.refresh),
	)
	g.status = widget.NewLabel("")

	g.hostsBox = container.NewVBox()
	scroll := container.NewVScroll(g.hostsBox)

	top := container.NewVBox(aliasRow, widget.NewSeparator(), toolbar, g.status, widget.NewSeparator())
	return container.NewBorder(top, nil, nil, nil, scroll)
}

func (g *gui) refresh() {
	st := g.core.State()
	if g.alias.Text == "" && st.ClientAlias != "" {
		g.alias.SetText(st.ClientAlias)
	}
	g.status.SetText(fmt.Sprintf("%s  ·  local ssh server: %s  ·  %d xray node(s)  ·  hosts from ~/.ssh/config",
		st.Platform, yn(st.LocalSSHOK), st.NodeCount))

	g.hostsBox.Objects = nil
	if len(st.Hosts) == 0 {
		g.hostsBox.Add(widget.NewLabel("No hosts in ~/.ssh/config yet — click “+ Add host”."))
	}
	for _, h := range st.Hosts {
		g.hostsBox.Add(g.hostCard(h))
	}
	g.hostsBox.Refresh()
}

func (g *gui) hostCard(h core.HostView) fyne.CanvasObject {
	alias := h.Alias
	title := widget.NewLabelWithStyle(alias+"   ("+target(h)+")",
		fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	rev := "no reverse tunnel"
	if h.HasReverse {
		rev = "reverse :" + h.ReversePort
	}
	meta := widget.NewLabel(fmt.Sprintf("%s  ·  %s  ·  tunnel: %s", rev, xrayLabel(h.HasProxy), stateLabel(h.Status)))

	// inline config toggles (checkboxes set OnChanged AFTER SetChecked so the
	// initial state doesn't fire an API call)
	revCheck := widget.NewCheck("reverse tunnel", nil)
	revCheck.SetChecked(h.HasReverse)
	revCheck.OnChanged = func(on bool) {
		port := 0
		if on {
			port = atoi(h.ReversePort, 2222)
		}
		g.do(func() error { return g.core.SetReverseTunnel(alias, port) })
	}
	xrayCheck := widget.NewCheck("xray", nil)
	xrayCheck.SetChecked(h.HasProxy)
	xrayCheck.OnChanged = func(on bool) { g.do(func() error { return g.core.SetProxy(alias, on) }) }
	autoCheck := widget.NewCheck("auto-start", nil)
	autoCheck.SetChecked(h.AutoStart)
	autoCheck.OnChanged = func(on bool) { g.do(func() error { return g.core.SetAutoStart(alias, on) }) }
	controls := container.NewHBox(revCheck, xrayCheck, autoCheck)

	start := widget.NewButton("Start", func() {
		g.do(func() error { _, err := g.core.StartTunnel(alias); return err })
	})
	stop := widget.NewButton("Stop", func() { g.core.StopTunnel(alias); g.refresh() })
	setup := widget.NewButton("Set up server", func() { g.showSetupServer(alias) })
	edit := widget.NewButton("Edit", func() { g.showEdit(h) })
	del := widget.NewButton("Delete", func() {
		dialog.ShowConfirm("Delete host", "Delete “"+alias+"” from ~/.ssh/config? This stops its tunnel.",
			func(ok bool) {
				if ok {
					g.do(func() error { return g.core.RemoveHost(alias) })
				}
			}, g.win)
	})
	actions := container.NewHBox(start, stop, setup, edit, del)

	return container.NewVBox(title, meta, controls, actions, widget.NewSeparator())
}

func target(h core.HostView) string {
	s := h.HostName
	if h.User != "" {
		s = h.User + "@" + s
	}
	if h.Port != "" {
		s += ":" + h.Port
	}
	return s
}

func (g *gui) showAddHost() {
	alias := widget.NewEntry()
	host := widget.NewEntry()
	user := widget.NewEntry()
	port := widget.NewEntry()
	port.SetText("22")
	items := []*widget.FormItem{
		widget.NewFormItem("Alias (ssh name)", alias),
		widget.NewFormItem("Host / IP", host),
		widget.NewFormItem("SSH user", user),
		widget.NewFormItem("SSH port", port),
	}
	dialog.ShowForm("Add host", "Add", "Cancel", items, func(ok bool) {
		if !ok {
			return
		}
		g.do(func() error {
			return g.core.AddHost(alias.Text, host.Text, user.Text, atoi(port.Text, 22))
		})
	}, g.win)
}

func (g *gui) showEdit(h core.HostView) {
	host := widget.NewEntry()
	host.SetText(h.HostName)
	user := widget.NewEntry()
	user.SetText(h.User)
	port := widget.NewEntry()
	port.SetText(h.Port)
	rport := widget.NewEntry()
	rport.SetText(h.ReversePort)
	items := []*widget.FormItem{
		widget.NewFormItem("Host / IP", host),
		widget.NewFormItem("SSH user", user),
		widget.NewFormItem("SSH port", port),
		widget.NewFormItem("Reverse port (blank = off)", rport),
	}
	alias := h.Alias
	dialog.ShowForm("Edit "+alias, "Save", "Cancel", items, func(ok bool) {
		if !ok {
			return
		}
		g.do(func() error {
			if err := g.core.SetParam(alias, "HostName", strings.TrimSpace(host.Text)); err != nil {
				return err
			}
			if err := g.core.SetParam(alias, "User", strings.TrimSpace(user.Text)); err != nil {
				return err
			}
			if err := g.core.SetParam(alias, "Port", strings.TrimSpace(port.Text)); err != nil {
				return err
			}
			return g.core.SetReverseTunnel(alias, atoi(rport.Text, 0))
		})
	}, g.win)
}

func (g *gui) showSetupServer(alias string) {
	pw := widget.NewPasswordEntry()
	pw.SetPlaceHolder("server password — first time only; empty if key/agent works")
	items := []*widget.FormItem{widget.NewFormItem("Password", pw)}
	dialog.ShowForm("Set up server", "Run", "Cancel", items, func(ok bool) {
		if !ok {
			return
		}
		res, err := g.core.SetupServer(alias, pw.Text)
		if err != nil {
			dialog.ShowError(err, g.win)
			return
		}
		dialog.ShowInformation("Server configured",
			fmt.Sprintf("Configured as %q. Its connect-back key was %s on this machine.",
				res.Alias, authLabel(res.Authorized)), g.win)
		g.refresh()
	}, g.win)
}

// do runs a mutating action, shows any error, and refreshes on success.
func (g *gui) do(fn func() error) {
	if err := fn(); err != nil {
		dialog.ShowError(err, g.win)
		return
	}
	g.refresh()
}

func (g *gui) showNodes() {
	raw, _ := g.core.Nodes()
	entry := widget.NewMultiLineEntry()
	entry.SetText(raw)
	entry.SetPlaceHolder("one vless:// URL per line; # comments allowed")
	entry.Wrapping = fyne.TextWrapOff
	d := dialog.NewCustomConfirm("Xray vless nodes", "Save", "Cancel",
		container.NewVScroll(entry), func(ok bool) {
			if !ok {
				return
			}
			if _, err := g.core.SetNodes(entry.Text); err != nil {
				dialog.ShowError(err, g.win)
				return
			}
			g.refresh()
		}, g.win)
	d.Resize(fyne.NewSize(540, 380))
	d.Show()
}

func (g *gui) showLocalSSHD() {
	disable := widget.NewCheck("also disable password login (recommended)", nil)
	disable.SetChecked(true)
	info := widget.NewLabel("Install/ensure the local ssh server so the agent can reach\n" +
		"back in. May prompt for sudo / Administrator in the terminal\n" +
		"where you launched the app.")
	dialog.ShowCustomConfirm("Local ssh server", "Install / ensure", "Cancel",
		container.NewVBox(info, disable), func(ok bool) {
			if !ok {
				return
			}
			running, err := g.core.EnsureLocalSSHD(disable.Checked)
			if err != nil {
				dialog.ShowError(err, g.win)
				return
			}
			dialog.ShowInformation("Local ssh server", "Done — running: "+yn(running), g.win)
			g.refresh()
		}, g.win)
}

func stateLabel(s bridge.Status) string {
	if s.State == bridge.StateRetrying && s.LastError != "" {
		return string(s.State) + " (" + s.LastError + ")"
	}
	return string(s.State)
}

func yn(b bool) string {
	if b {
		return "running"
	}
	return "not detected"
}

func xrayLabel(b bool) string {
	if b {
		return "xray on"
	}
	return "direct"
}

func authLabel(b bool) string {
	if b {
		return "authorized"
	}
	return "already present"
}

func atoi(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return def
	}
	return n
}
