//go:build gui

// Command remote-claude-gui is the native desktop front-end. It builds the same
// core.App the web UI drives and renders it with Fyne. Build with:
//
//	CGO_ENABLED=1 go build -tags gui ./cmd/remote-claude-gui
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
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

	"github.com/papasaidfine/remote-claude/internal/autostart"
	"github.com/papasaidfine/remote-claude/internal/bridge"
	"github.com/papasaidfine/remote-claude/internal/core"
	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/platform"
	"github.com/papasaidfine/remote-claude/internal/provision"
	"github.com/papasaidfine/remote-claude/internal/relay"
	"github.com/papasaidfine/remote-claude/internal/sshbin"
	"github.com/papasaidfine/remote-claude/internal/store"
	"github.com/papasaidfine/remote-claude/internal/usage"
)

// version is stamped at release time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	// ssh invokes this same binary headlessly for the xray ProxyCommand relay and
	// the SSH_ASKPASS helper (a host's ProxyCommand points at whichever binary
	// wrote it — possibly this GUI). Handle those and exit BEFORE opening a window,
	// or starting a tunnel would spawn a second app window.
	if os.Getenv("RC_ASKPASS_MODE") == "1" {
		fmt.Println(os.Getenv("RC_ASKPASS_SECRET"))
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "relay" {
		os.Exit(relay.Main(os.Args[2:]))
	}

	closeLog := setupLog()
	defer closeLog()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic: %v\n%s", r, debug.Stack())
			os.Exit(1)
		}
	}()
	run()
}

// setupLog points the standard logger at a file next to the config, so a GUI
// crash (there is no console on Windows) leaves a trace the user can send. It is
// best-effort: failures are ignored.
func setupLog() func() {
	dir := os.TempDir()
	if p, err := paths.Resolve(); err == nil {
		dir = p.RCConfigDir
	}
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(filepath.Join(dir, "gui.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return func() {}
	}
	log.SetOutput(f)
	log.Printf("remote-claude-gui %s starting", version)
	return func() { f.Close() }
}

func run() {
	p, err := paths.Resolve()
	if err != nil {
		die(err)
	}
	cfg, _ := store.Load(store.Path(p)) // tolerant: never nil, never fatal
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
	log.Printf("fatal: %v", err)
	fmt.Fprintln(os.Stderr, "remote-claude-gui:", err)
	os.Exit(1)
}

type gui struct {
	core     *core.App
	win      fyne.Window
	alias    *widget.Entry
	aliasBtn *widget.Button
	status   *widget.Label
	hostsBox *fyne.Container
}

func (g *gui) build() fyne.CanvasObject {
	// This machine's name: locked (read-only) until you click Edit.
	g.alias = widget.NewEntry()
	g.alias.SetPlaceHolder("this machine's name, e.g. lc-pc")
	g.alias.Disable()
	g.aliasBtn = widget.NewButton("Edit", g.toggleAliasEdit)
	aliasRow := container.NewBorder(nil, nil, widget.NewLabel("This machine's name"), g.aliasBtn, g.alias)

	// Start-on-login (OnChanged set after SetChecked so the initial state doesn't
	// fire a write).
	autoLaunch := widget.NewCheck("Start this app when I log in", nil)
	autoLaunch.SetChecked(autostart.Enabled())
	autoLaunch.OnChanged = func(on bool) {
		if err := autostart.SetEnabled(on); err != nil {
			dialog.ShowError(err, g.win)
			autoLaunch.SetChecked(autostart.Enabled())
		}
	}

	toolbar := container.NewHBox(
		widget.NewButton("+ Add host", g.showAddHost),
		widget.NewButton("Xray", g.showXray),
		widget.NewButton("Local ssh server", g.showLocalSSHD),
		widget.NewButton("Refresh", g.refresh),
	)
	g.status = widget.NewLabel("")

	g.hostsBox = container.NewVBox()
	scroll := container.NewVScroll(g.hostsBox)

	top := container.NewVBox(aliasRow, autoLaunch, widget.NewSeparator(), toolbar, g.status, widget.NewSeparator())
	return container.NewBorder(top, nil, nil, nil, scroll)
}

// toggleAliasEdit flips the name field between read-only and editing; saving on
// the second click.
func (g *gui) toggleAliasEdit() {
	if g.alias.Disabled() {
		g.alias.Enable()
		g.aliasBtn.SetText("Save")
		return
	}
	if _, err := g.core.SetAlias(g.alias.Text); err != nil {
		dialog.ShowError(err, g.win)
		return
	}
	g.alias.Disable()
	g.aliasBtn.SetText("Edit")
	g.refresh()
}

func (g *gui) refresh() {
	st := g.core.State()
	if g.alias.Disabled() { // keep the locked field in sync; don't clobber an edit
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

	xrayCheck := widget.NewCheck("route through xray", nil)
	xrayCheck.SetChecked(h.HasProxy)
	xrayCheck.OnChanged = func(on bool) { g.do(func() error { return g.core.SetProxy(alias, on) }) }

	edit := widget.NewButton("Edit", func() { g.showEdit(h) })
	usageBtn := widget.NewButton("Usage", func() { g.showUsage(alias) })
	del := widget.NewButton("Delete", func() {
		dialog.ShowConfirm("Delete host", "Delete “"+alias+"” from ~/.ssh/config? This stops its tunnel.",
			func(ok bool) {
				if ok {
					g.do(func() error { return g.core.RemoveHost(alias) })
				}
			}, g.win)
	})

	rows := []fyne.CanvasObject{title}
	if h.HasReverse {
		// A tunnel host: full tunnel controls.
		rows = append(rows, widget.NewLabel(fmt.Sprintf("reverse tunnel :%d  ·  tunnel: %s",
			h.ReversePort, stateLabel(h.Status))))
		start := widget.NewButton("Start", func() {
			g.do(func() error { _, err := g.core.StartTunnel(alias); return err })
		})
		stop := widget.NewButton("Stop", func() { g.core.StopTunnel(alias); g.refresh() })
		if h.Status.State == bridge.StateStopped {
			stop.Disable()
		} else {
			start.SetText("Restart")
		}
		setup := widget.NewButton("Set up server", func() { g.showSetupServer(alias) })
		auto := widget.NewCheck("start tunnel when app opens", nil)
		auto.SetChecked(h.AutoStart)
		auto.OnChanged = func(on bool) { g.do(func() error { return g.core.SetAutoStart(alias, on) }) }
		rows = append(rows,
			container.NewHBox(start, stop, setup, usageBtn),
			container.NewHBox(xrayCheck, auto, edit, del))
	} else {
		// A plain ssh host: just show it and offer to make it a tunnel host.
		rows = append(rows,
			widget.NewLabelWithStyle("plain ssh host — no reverse tunnel",
				fyne.TextAlignLeading, fyne.TextStyle{Italic: true}),
			container.NewHBox(
				widget.NewButton("Enable reverse tunnel", func() {
					g.do(func() error { return g.core.SetReverseTunnel(alias, 2222) })
				}),
				usageBtn, xrayCheck, edit, del))
	}
	rows = append(rows, widget.NewSeparator())
	return container.NewVBox(rows...)
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
	d := dialog.NewForm("Add host", "Add", "Cancel", items, func(ok bool) {
		if !ok {
			return
		}
		g.do(func() error {
			return g.core.AddHost(alias.Text, host.Text, user.Text, atoi(port.Text, 22))
		})
	}, g.win)
	d.Resize(fyne.NewSize(460, 300)) // wide enough to see a full IP
	d.Show()
}

func (g *gui) showEdit(h core.HostView) {
	host := widget.NewEntry()
	host.SetText(h.HostName)
	user := widget.NewEntry()
	user.SetText(h.User)
	port := widget.NewEntry()
	port.SetText(h.Port)
	rport := widget.NewEntry()
	if h.ReversePort > 0 {
		rport.SetText(strconv.Itoa(h.ReversePort))
	}
	items := []*widget.FormItem{
		widget.NewFormItem("Host / IP", host),
		widget.NewFormItem("SSH user", user),
		widget.NewFormItem("SSH port", port),
		widget.NewFormItem("Reverse port (blank = off)", rport),
	}
	alias := h.Alias
	d := dialog.NewForm("Edit "+alias, "Save", "Cancel", items, func(ok bool) {
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
			// Reverse tunnel is app metadata, not ssh config: blank/0 turns it off.
			return g.core.SetReverseTunnel(alias, atoi(rport.Text, 0))
		})
	}, g.win)
	d.Resize(fyne.NewSize(460, 340))
	d.Show()
}

// showSetupServer tries key/agent auth first (no prompt) and only asks for a
// password if that fails — the first-time authorization case. The ssh work runs
// off the UI thread so the window stays responsive.
func (g *gui) showSetupServer(alias string) {
	go func() {
		res, err := g.core.SetupServer(alias, "")
		fyne.Do(func() {
			switch {
			case err == nil:
				g.setupDone(res)
			case isAuthError(err):
				g.promptSetupPassword(alias) // key genuinely not authorized yet
			default:
				dialog.ShowError(err, g.win) // some other failure — show the real error
			}
		})
	}()
}

// isAuthError reports whether an ssh failure is a public-key rejection (vs. a
// connection error, a server-side script failure, etc.).
func isAuthError(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "permission denied") || strings.Contains(s, "publickey")
}

func (g *gui) promptSetupPassword(alias string) {
	pw := widget.NewPasswordEntry()
	info := widget.NewLabel("The server rejected your key — it isn't authorized there yet.\n" +
		"If the server accepts a password, enter it to authorize your key.\n" +
		"(Key-only server? Cancel and add ~/.ssh/id_ed25519.pub to its\nauthorized_keys yourself.)")
	dialog.ShowCustomConfirm("Set up server", "Authorize with password", "Cancel",
		container.NewVBox(info, pw), func(ok bool) {
			if !ok {
				return
			}
			go func() {
				res, err := g.core.SetupServer(alias, pw.Text)
				fyne.Do(func() {
					if err != nil {
						dialog.ShowError(err, g.win)
						return
					}
					g.setupDone(res)
				})
			}()
		}, g.win)
}

func (g *gui) setupDone(res provision.ServerResult) {
	dialog.ShowInformation("Server configured",
		fmt.Sprintf("Configured as %q. Its connect-back key was %s on this machine.",
			res.Alias, authLabel(res.Authorized)), g.win)
	g.refresh()
}

// showUsage fetches Claude usage from the host over ssh (off the UI thread) and
// shows a 1D/7D/30D tabbed, priced breakdown.
func (g *gui) showUsage(alias string) {
	body := container.NewStack(container.NewPadded(
		widget.NewLabel("Reading Claude usage from " + alias + " …")))
	d := dialog.NewCustom("Claude usage — "+alias, "Close", body, g.win)
	d.Resize(fyne.NewSize(640, 480))
	d.Show()
	go func() {
		rep, err := g.core.HostUsage(alias)
		fyne.Do(func() {
			if err != nil {
				body.Objects = []fyne.CanvasObject{container.NewPadded(widget.NewLabel("Failed: " + err.Error()))}
			} else {
				body.Objects = []fyne.CanvasObject{usageTabs(rep)}
			}
			body.Refresh()
		})
	}()
}

func usageTabs(rep usage.Report) fyne.CanvasObject {
	return container.NewAppTabs(
		container.NewTabItem("Past 1 day", usageWindow(rep.Day)),
		container.NewTabItem("Past 7 days", usageWindow(rep.Week)),
		container.NewTabItem("Past 30 days", usageWindow(rep.Month)),
	)
}

func usageWindow(w usage.Window) fyne.CanvasObject {
	if len(w.Models) == 0 {
		return container.NewPadded(widget.NewLabel("No usage in this window."))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-22s %9s %9s %9s %9s %10s\n", "Model", "Input", "Output", "CacheW", "CacheR", "Cost")
	for _, m := range w.Models {
		fmt.Fprintf(&b, "%-22s %9s %9s %9s %9s %10s\n",
			shortModel(m.Model), tok(m.Tokens.Input), tok(m.Tokens.Output),
			tok(m.Tokens.CacheWrite), tok(m.Tokens.CacheRead), "$"+money(m.Cost))
	}
	fmt.Fprintf(&b, "%-22s %9s %9s %9s %9s %10s\n", "TOTAL",
		tok(w.Total.Input), tok(w.Total.Output), tok(w.Total.CacheWrite), tok(w.Total.CacheRead), "$"+money(w.Cost))
	return container.NewVScroll(widget.NewTextGridFromString(b.String()))
}

func tok(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1e3)
	default:
		return strconv.FormatInt(n, 10)
	}
}

func money(f float64) string { return fmt.Sprintf("%.2f", f) }

func shortModel(s string) string {
	s = strings.TrimPrefix(s, "claude-")
	if i := strings.IndexByte(s, '['); i >= 0 {
		s = s[:i]
	}
	if len(s) > 22 {
		s = s[:22]
	}
	return s
}

// do runs a mutating action, shows any error, and refreshes on success.
func (g *gui) do(fn func() error) {
	if err := fn(); err != nil {
		dialog.ShowError(err, g.win)
		return
	}
	g.refresh()
}

func (g *gui) showXray() {
	proxy := widget.NewEntry()
	proxy.SetPlaceHolder("download via proxy, e.g. http://127.0.0.1:7890 (optional, one-time)")
	status := widget.NewLabel("")
	var download *widget.Button
	download = widget.NewButton("Download / update xray", func() {
		download.Disable()
		status.SetText("Downloading… (this can take a moment)")
		p := strings.TrimSpace(proxy.Text)
		go func() {
			err := g.core.InstallXray(p)
			fyne.Do(func() {
				download.Enable()
				if err != nil {
					status.SetText("Failed: " + err.Error())
					return
				}
				status.SetText("xray ready.")
				proxy.SetText("")
				g.refresh()
			})
		}()
	})

	nodesEntry := widget.NewMultiLineEntry()
	raw, _ := g.core.Nodes()
	nodesEntry.SetText(raw)
	nodesEntry.SetPlaceHolder("one vless:// URL per line; # comments allowed")
	nodesEntry.Wrapping = fyne.TextWrapOff

	top := container.NewVBox(
		container.NewBorder(nil, nil, nil, download, proxy),
		status,
		widget.NewLabel("Nodes (one vless:// per line):"),
	)
	content := container.NewBorder(top, nil, nil, nil, container.NewVScroll(nodesEntry))

	d := dialog.NewCustomConfirm("Xray", "Save nodes", "Close", content, func(ok bool) {
		if !ok {
			return
		}
		if _, err := g.core.SetNodes(nodesEntry.Text); err != nil {
			dialog.ShowError(err, g.win)
			return
		}
		g.refresh()
	}, g.win)
	d.Resize(fyne.NewSize(580, 480))
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
