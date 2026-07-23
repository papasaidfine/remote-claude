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
	"github.com/papasaidfine/remote-claude/internal/i18n"
	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/platform"
	"github.com/papasaidfine/remote-claude/internal/provision"
	"github.com/papasaidfine/remote-claude/internal/relay"
	"github.com/papasaidfine/remote-claude/internal/selfupdate"
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
	selfupdate.CleanupOldBinary() // sweep a "<exe>.old" left by a prior self-update
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
	w.Resize(fyne.NewSize(720, 640))

	lang := i18n.Parse(appCore.Lang())
	g := &gui{core: appCore, app: a, win: w, lang: lang, pr: i18n.P(lang)}
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
	app      fyne.App
	win      fyne.Window
	lang     i18n.Lang
	pr       i18n.Printer
	alias    *widget.Entry
	aliasBtn *widget.Button
	status   *widget.Label
	hostsBox *fyne.Container
}

// t translates key into the active UI language.
func (g *gui) t(key string) string { return g.pr.T(key) }

// applyLang switches the UI language, persists it, and rebuilds the window.
func (g *gui) applyLang(l i18n.Lang) {
	if l == g.lang {
		return
	}
	g.lang = l
	g.pr = i18n.P(l)
	_ = g.core.SetLang(string(l))
	g.win.SetContent(g.build())
	g.refresh()
}

func (g *gui) build() fyne.CanvasObject {
	// This machine's name: locked (read-only) until you click Edit.
	g.alias = widget.NewEntry()
	g.alias.SetPlaceHolder(g.t("machine_name_ph"))
	g.alias.Disable()
	g.aliasBtn = widget.NewButton(g.t("edit"), g.toggleAliasEdit)
	aliasRow := container.NewBorder(nil, nil, widget.NewLabel(g.t("machine_name_label")), g.aliasBtn, g.alias)

	// Start-on-login (OnChanged set after SetChecked so the initial state doesn't
	// fire a write).
	autoLaunch := widget.NewCheck(g.t("start_on_login"), nil)
	autoLaunch.SetChecked(autostart.Enabled())
	autoLaunch.OnChanged = func(on bool) {
		if err := autostart.SetEnabled(on); err != nil {
			dialog.ShowError(err, g.win)
			autoLaunch.SetChecked(autostart.Enabled())
		}
	}

	// Language picker (OnChanged assigned after SetSelected so it doesn't fire).
	var opts []string
	for _, l := range i18n.Available {
		opts = append(opts, l.Name())
	}
	langSel := widget.NewSelect(opts, nil)
	langSel.SetSelected(g.lang.Name())
	langSel.OnChanged = func(name string) {
		for _, l := range i18n.Available {
			if l.Name() == name {
				g.applyLang(l)
				return
			}
		}
	}
	langRow := container.NewHBox(widget.NewLabel(g.t("language")), langSel)

	toolbar := container.NewHBox(
		widget.NewButton(g.t("add_host"), g.showAddHost),
		widget.NewButton("Xray", g.showXray),
		widget.NewButton(g.t("local_ssh_server"), g.showLocalSSHD),
		widget.NewButton(g.t("refresh"), g.refresh),
		widget.NewButton(g.t("check_update"), g.checkUpdate),
	)
	g.status = widget.NewLabel("")

	g.hostsBox = container.NewVBox()
	scroll := container.NewVScroll(g.hostsBox)

	top := container.NewVBox(aliasRow, autoLaunch, langRow, widget.NewSeparator(), toolbar, g.status, widget.NewSeparator())
	return container.NewBorder(top, nil, nil, nil, scroll)
}

// toggleAliasEdit flips the name field between read-only and editing; saving on
// the second click.
func (g *gui) toggleAliasEdit() {
	if g.alias.Disabled() {
		g.alias.Enable()
		g.aliasBtn.SetText(g.t("save"))
		return
	}
	if _, err := g.core.SetAlias(g.alias.Text); err != nil {
		dialog.ShowError(err, g.win)
		return
	}
	g.alias.Disable()
	g.aliasBtn.SetText(g.t("edit"))
	g.refresh()
}

func (g *gui) refresh() {
	st := g.core.State()
	if g.alias.Disabled() { // keep the locked field in sync; don't clobber an edit
		g.alias.SetText(st.ClientAlias)
	}
	g.status.SetText(fmt.Sprintf(g.t("status_fmt"),
		st.Platform, g.yn(st.LocalSSHOK), st.NodeCount))

	g.hostsBox.Objects = nil
	if len(st.Hosts) == 0 {
		g.hostsBox.Add(widget.NewLabel(g.t("no_hosts")))
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

	xrayCheck := widget.NewCheck(g.t("route_xray"), nil)
	xrayCheck.SetChecked(h.HasProxy)
	xrayCheck.OnChanged = func(on bool) { g.do(func() error { return g.core.SetProxy(alias, on) }) }

	edit := widget.NewButton(g.t("edit"), func() { g.showEdit(h) })
	usageBtn := widget.NewButton(g.t("usage"), func() { g.showUsage(alias) })
	del := widget.NewButton(g.t("delete"), func() {
		dialog.ShowConfirm(g.t("delete_host_title"), fmt.Sprintf(g.t("delete_host_conf_fmt"), alias),
			func(ok bool) {
				if ok {
					g.do(func() error { return g.core.RemoveHost(alias) })
				}
			}, g.win)
	})

	rows := []fyne.CanvasObject{title}
	if h.HasReverse {
		// A tunnel host: full tunnel controls.
		rows = append(rows, widget.NewLabel(fmt.Sprintf(g.t("reverse_status_fmt"),
			h.ReversePort, g.stateLabel(h.Status))))
		start := widget.NewButton(g.t("start"), func() {
			g.do(func() error { _, err := g.core.StartTunnel(alias); return err })
		})
		stop := widget.NewButton(g.t("stop"), func() { g.core.StopTunnel(alias); g.refresh() })
		if h.Status.State == bridge.StateStopped {
			stop.Disable()
		} else {
			start.SetText(g.t("restart"))
		}
		setup := widget.NewButton(g.t("setup_server"), func() { g.showSetupServer(alias) })
		auto := widget.NewCheck(g.t("auto_start_tunnel"), nil)
		auto.SetChecked(h.AutoStart)
		auto.OnChanged = func(on bool) { g.do(func() error { return g.core.SetAutoStart(alias, on) }) }
		rows = append(rows,
			container.NewHBox(start, stop, setup, usageBtn),
			container.NewHBox(xrayCheck, auto, edit, del))
	} else {
		// A plain ssh host: just show it and offer to make it a tunnel host.
		rows = append(rows,
			widget.NewLabelWithStyle(g.t("plain_host"),
				fyne.TextAlignLeading, fyne.TextStyle{Italic: true}),
			container.NewHBox(
				widget.NewButton(g.t("enable_reverse"), func() {
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
		widget.NewFormItem(g.t("alias_ssh_name"), alias),
		widget.NewFormItem(g.t("host_ip"), host),
		widget.NewFormItem(g.t("ssh_user"), user),
		widget.NewFormItem(g.t("ssh_port"), port),
	}
	d := dialog.NewForm(g.t("add_host_title"), g.t("add"), g.t("cancel"), items, func(ok bool) {
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
		widget.NewFormItem(g.t("host_ip"), host),
		widget.NewFormItem(g.t("ssh_user"), user),
		widget.NewFormItem(g.t("ssh_port"), port),
		widget.NewFormItem(g.t("reverse_port"), rport),
	}
	alias := h.Alias
	d := dialog.NewForm(fmt.Sprintf(g.t("edit_title_fmt"), alias), g.t("save"), g.t("cancel"), items, func(ok bool) {
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
	info := widget.NewLabel(g.t("setup_pw_info"))
	dialog.ShowCustomConfirm(g.t("setup_server"), g.t("setup_pw_authorize"), g.t("cancel"),
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
	dialog.ShowInformation(g.t("server_configured"),
		fmt.Sprintf(g.t("server_conf_fmt"), res.Alias, g.authLabel(res.Authorized)), g.win)
	g.refresh()
}

// showUsage fetches Claude usage from the host over ssh (off the UI thread) and
// shows a 1D/7D/30D tabbed, priced breakdown.
func (g *gui) showUsage(alias string) {
	body := container.NewStack(container.NewPadded(
		widget.NewLabel(fmt.Sprintf(g.t("reading_usage_fmt"), alias))))
	d := dialog.NewCustom(fmt.Sprintf(g.t("usage_title_fmt"), alias), g.t("close"), body, g.win)
	d.Resize(fyne.NewSize(640, 480))
	d.Show()
	go func() {
		rep, err := g.core.HostUsage(alias)
		fyne.Do(func() {
			if err != nil {
				body.Objects = []fyne.CanvasObject{container.NewPadded(widget.NewLabel(fmt.Sprintf(g.t("failed_fmt"), err.Error())))}
			} else {
				body.Objects = []fyne.CanvasObject{g.usageTabs(rep)}
			}
			body.Refresh()
		})
	}()
}

func (g *gui) usageTabs(rep usage.Report) fyne.CanvasObject {
	return container.NewAppTabs(
		container.NewTabItem(g.t("past_1d"), g.usageWindow(rep.Day)),
		container.NewTabItem(g.t("past_7d"), g.usageWindow(rep.Week)),
		container.NewTabItem(g.t("past_30d"), g.usageWindow(rep.Month)),
	)
}

func (g *gui) usageWindow(w usage.Window) fyne.CanvasObject {
	if len(w.Models) == 0 {
		return container.NewPadded(widget.NewLabel(g.t("no_usage_window")))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-22s %9s %9s %9s %9s %10s\n",
		g.t("col_model"), g.t("col_input"), g.t("col_output"), g.t("col_cache_w"), g.t("col_cache_r"), g.t("col_cost"))
	for _, m := range w.Models {
		fmt.Fprintf(&b, "%-22s %9s %9s %9s %9s %10s\n",
			shortModel(m.Model), tok(m.Tokens.Input), tok(m.Tokens.Output),
			tok(m.Tokens.CacheWrite), tok(m.Tokens.CacheRead), "$"+money(m.Cost))
	}
	fmt.Fprintf(&b, "%-22s %9s %9s %9s %9s %10s\n", g.t("col_total"),
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
	proxy.SetPlaceHolder(g.t("xray_proxy_ph"))
	status := widget.NewLabel("")
	var download *widget.Button
	download = widget.NewButton(g.t("xray_download"), func() {
		download.Disable()
		status.SetText(g.t("downloading"))
		p := strings.TrimSpace(proxy.Text)
		go func() {
			err := g.core.InstallXray(p)
			fyne.Do(func() {
				download.Enable()
				if err != nil {
					status.SetText(fmt.Sprintf(g.t("failed_fmt"), err.Error()))
					return
				}
				status.SetText(g.t("xray_ready"))
				proxy.SetText("")
				g.refresh()
			})
		}()
	})

	nodesEntry := widget.NewMultiLineEntry()
	raw, _ := g.core.Nodes()
	nodesEntry.SetText(raw)
	nodesEntry.SetPlaceHolder(g.t("nodes_ph"))
	nodesEntry.Wrapping = fyne.TextWrapOff

	top := container.NewVBox(
		container.NewBorder(nil, nil, nil, download, proxy),
		status,
		widget.NewLabel(g.t("nodes_label")),
	)
	content := container.NewBorder(top, nil, nil, nil, container.NewVScroll(nodesEntry))

	d := dialog.NewCustomConfirm("Xray", g.t("save_nodes"), g.t("close"), content, func(ok bool) {
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
	disable := widget.NewCheck(g.t("sshd_disable_pw"), nil)
	disable.SetChecked(true)
	info := widget.NewLabel(g.t("sshd_info"))
	dialog.ShowCustomConfirm(g.t("local_ssh_server"), g.t("sshd_install"), g.t("cancel"),
		container.NewVBox(info, disable), func(ok bool) {
			if !ok {
				return
			}
			running, err := g.core.EnsureLocalSSHD(disable.Checked)
			if err != nil {
				dialog.ShowError(err, g.win)
				return
			}
			dialog.ShowInformation(g.t("local_ssh_server"), fmt.Sprintf(g.t("sshd_done_fmt"), g.yn(running)), g.win)
			g.refresh()
		}, g.win)
}

// checkUpdate queries GitHub for a newer release and, if one exists, offers to
// download and install it. Network work runs off the UI thread.
func (g *gui) checkUpdate() {
	prog := dialog.NewCustom(g.t("update_title"), g.t("close"),
		widget.NewLabel(g.t("update_checking")), g.win)
	prog.Show()
	go func() {
		rel, err := selfupdate.Check(version)
		fyne.Do(func() {
			prog.Hide()
			switch {
			case err != nil:
				dialog.ShowError(fmt.Errorf(g.t("update_failed_fmt"), err), g.win)
			case version == "dev":
				dialog.ShowInformation(g.t("update_title"), g.t("update_dev"), g.win)
			case !rel.HasUpdate:
				dialog.ShowInformation(g.t("update_title"), fmt.Sprintf(g.t("update_latest_fmt"), version), g.win)
			default:
				dialog.ShowCustomConfirm(g.t("update_title"), g.t("update_download_yes"), g.t("cancel"),
					widget.NewLabel(fmt.Sprintf(g.t("update_avail_fmt"), version, rel.Version)),
					func(ok bool) {
						if ok {
							g.applyUpdate()
						}
					}, g.win)
			}
		})
	}()
}

// applyUpdate downloads and installs the latest release, then offers a restart.
func (g *gui) applyUpdate() {
	prog := dialog.NewCustom(g.t("update_title"), g.t("close"),
		widget.NewLabel(g.t("update_downloading")), g.win)
	prog.Show()
	go func() {
		err := selfupdate.Apply("")
		fyne.Do(func() {
			prog.Hide()
			if err != nil {
				dialog.ShowError(fmt.Errorf(g.t("update_failed_fmt"), err), g.win)
				return
			}
			dialog.ShowCustomConfirm(g.t("update_title"), g.t("restart_now"), g.t("later"),
				widget.NewLabel(g.t("update_done")), func(ok bool) {
					if !ok {
						return
					}
					if err := selfupdate.Restart(); err != nil {
						dialog.ShowError(err, g.win)
						return
					}
					g.app.Quit() // triggers OnStopped → tunnels stop; the new process takes over
				}, g.win)
		})
	}()
}

func (g *gui) stateLabel(s bridge.Status) string {
	name := g.stateName(s.State)
	if s.State == bridge.StateRetrying && s.LastError != "" {
		return name + " (" + s.LastError + ")"
	}
	return name
}

func (g *gui) stateName(st bridge.State) string {
	switch st {
	case bridge.StateStopped:
		return g.t("state_stopped")
	case bridge.StateConnecting:
		return g.t("state_connecting")
	case bridge.StateUp:
		return g.t("state_up")
	case bridge.StateRetrying:
		return g.t("state_retrying")
	}
	return string(st)
}

func (g *gui) yn(b bool) string {
	if b {
		return g.t("running")
	}
	return g.t("not_detected")
}

func (g *gui) authLabel(b bool) string {
	if b {
		return g.t("authorized")
	}
	return g.t("already_present")
}

func atoi(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return def
	}
	return n
}
