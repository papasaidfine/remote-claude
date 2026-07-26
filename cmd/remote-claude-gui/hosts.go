//go:build gui

package main

import (
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/papasaidfine/remote-claude/internal/bridge"
	"github.com/papasaidfine/remote-claude/internal/core"
	"github.com/papasaidfine/remote-claude/internal/provision"
)

// buildHosts lays out the Hosts tab: who this machine is, then one card per
// host in ~/.ssh/config.
func (g *gui) buildHosts() fyne.CanvasObject {
	// This machine's name: locked (read-only) until you click Edit.
	g.alias = widget.NewEntry()
	g.alias.SetPlaceHolder(g.t("machine_name_ph"))
	g.alias.Disable()
	g.aliasBtn = widget.NewButton(g.t("edit"), g.toggleAliasEdit)
	aliasRow := container.NewBorder(nil, nil,
		widget.NewLabelWithStyle(g.t("machine_name_label"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		g.aliasBtn, g.alias)

	g.status = widget.NewLabel("")
	g.status.Importance = widget.LowImportance

	add := widget.NewButtonWithIcon(g.t("add_host"), theme.ContentAddIcon(), g.showAddHost)
	add.Importance = widget.HighImportance
	refresh := widget.NewButtonWithIcon(g.t("refresh"), theme.ViewRefreshIcon(), g.refresh)
	toolbar := container.NewHBox(add, refresh, layout.NewSpacer())

	g.hostsBox = container.NewVBox()
	// Cards cache their captions in the current language and belong to the box
	// built above, so a rebuild (start-up, or a language switch) starts empty.
	g.rows = map[string]*hostRow{}
	g.empty = nil

	top := container.NewVBox(aliasRow, g.status, toolbar, widget.NewSeparator())
	return container.NewPadded(
		container.NewBorder(top, nil, nil, nil, container.NewVScroll(g.hostsBox)))
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

func (g *gui) refreshHosts(st core.State) {
	if g.alias.Disabled() && g.alias.Text != st.ClientAlias { // don't clobber an edit
		g.alias.SetText(st.ClientAlias)
	}
	setLabel(g.status, fmt.Sprintf(g.t("status_fmt"),
		st.Platform, g.yn(st.LocalSSHOK), st.NodeCount))
	g.syncHosts(st.Hosts)
}

// syncHosts brings the card list in line with hosts, reusing every card whose
// host is still there in the same layout variant. Rebuilding the list instead —
// which is what this used to do, twice a minute — strands the old widget tree in
// Fyne's renderer cache, and that cache is only swept when a window actually
// repaints (internal/cache.Clean only calls destroyExpiredRenderers on a canvas
// refresh). Hidden in the system tray, nothing ever repaints, so every card ever
// built stayed live: ~14MB/min, gigabytes over a day.
func (g *gui) syncHosts(hosts []core.HostView) {
	objs := make([]fyne.CanvasObject, 0, len(hosts))
	seen := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		if seen[h.Alias] {
			// Belt and braces: core.State already collapses a repeated ssh-config
			// alias, and appending one card object twice would leave a hole the
			// size of a card where the second slot is.
			continue
		}
		seen[h.Alias] = true
		row, ok := g.rows[h.Alias]
		if !ok || row.hasRev != h.HasReverse {
			row = g.newHostRow(h)
			g.rows[h.Alias] = row
		} else {
			row.update(g, h)
		}
		objs = append(objs, row.root)
	}
	for alias := range g.rows {
		if !seen[alias] {
			delete(g.rows, alias)
		}
	}
	if len(objs) == 0 {
		if g.empty == nil {
			g.empty = widget.NewLabel(g.t("no_hosts"))
		}
		objs = append(objs, g.empty)
	}
	if sameObjects(g.hostsBox.Objects, objs) {
		return // same cards in the same order; the in-place updates were enough
	}
	g.hostsBox.Objects = objs
	g.hostsBox.Refresh()
}

func sameObjects(a, b []fyne.CanvasObject) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// hostRow is one host's card. It is built once and then updated in place; the
// widgets whose contents change on a tick are kept as fields.
type hostRow struct {
	root   fyne.CanvasObject
	cur    core.HostView // latest state — dialogs read this, not a stale capture
	hasRev bool          // layout variant this card was built for
	dot    *canvas.Circle
	title  *widget.Label
	status *widget.Label    // tunnel hosts only
	toggle *widget.Button   // tunnel hosts only: start and stop, one control
	spin   *widget.Activity // tunnel hosts only: shown while not yet settled
}

func (g *gui) newHostRow(h core.HostView) *hostRow {
	r := &hostRow{cur: h, hasRev: h.HasReverse}
	alias := h.Alias

	// The state marker is a drawn circle, not a "●" glyph: the bundled font is
	// subset to the ranges the UI actually uses (see theme.go) and U+25CF is not
	// among them, so the character would render as tofu.
	r.dot = canvas.NewCircle(dimColor(g.app))
	dot := container.NewCenter(
		container.New(layout.NewGridWrapLayout(fyne.NewSize(10, 10)), r.dot))

	r.title = widget.NewLabelWithStyle(alias, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	where := widget.NewLabel(target(h))
	where.Importance = widget.LowImportance
	titleRow := container.NewBorder(nil, nil, container.NewHBox(dot, r.title), where)

	// Launch actions: open the host in an editor, or hand over the command to
	// work in it from a terminal. The command is copied rather than run — there
	// is no portable way to open "the user's terminal", and picking one for them
	// would be wrong as often as it was right.
	// Icon only — the marks carry the meaning, and two more labelled buttons
	// would not fit the row. Fyne has no tooltips, so the status line names the
	// action after a click instead.
	vscode := widget.NewButtonWithIcon("", vscodeIcon, func() {
		setLabel(g.status, fmt.Sprintf(g.t("opening_vscode_fmt"), alias))
		// The first click for a host asks it where its home directory is, which
		// is an ssh round-trip; later ones are immediate. Off the UI thread
		// either way, so a slow or unreachable host cannot freeze the window.
		go func() {
			err := g.core.OpenVSCode(alias)
			fyne.Do(func() {
				if err != nil {
					dialog.ShowError(err, g.win)
				}
			})
		}()
	})
	cli := widget.NewButtonWithIcon("", clawdIcon, func() {
		cmd := core.ClaudeCommand(alias)
		g.app.Clipboard().SetContent(cmd)
		setLabel(g.status, fmt.Sprintf(g.t("cli_copied_fmt"), cmd))
	})

	usageBtn := widget.NewButton(g.t("usage"), func() { g.showUsage(alias) })
	edit := widget.NewButton(g.t("edit"), func() { g.showEdit(r.cur) })
	del := widget.NewButton(g.t("delete"), func() {
		dialog.ShowConfirm(g.t("delete_host_title"), fmt.Sprintf(g.t("delete_host_conf_fmt"), alias),
			func(ok bool) {
				if ok {
					g.do(func() error { return g.core.RemoveHost(alias) })
				}
			}, g.win)
	})
	// Left plain on purpose. Painting it red put two saturated buttons on every
	// card, and the louder of the two was the one you rarely want — the eye went
	// to Delete instead of Start. The confirmation dialog is what guards it.

	rows := []fyne.CanvasObject{titleRow}
	if h.HasReverse {
		// A tunnel host: full tunnel controls.
		r.status = widget.NewLabel("")
		r.status.Importance = widget.LowImportance
		// One control for the whole tunnel lifecycle. Start and Stop were two
		// buttons of which one was always the wrong one to press, and a separate
		// Restart on top of that; what the row actually has to say is whether the
		// tunnel is on, so it is a toggle. While the state is unsettled a spinner
		// stands beside it — and the toggle still works, because a tunnel stuck
		// retrying behind a long backoff has to be stoppable.
		r.toggle = widget.NewButtonWithIcon(g.t("start"), theme.MediaPlayIcon(), func() {
			if r.cur.Status.State == bridge.StateStopped {
				g.do(func() error { _, err := g.core.StartTunnel(alias); return err })
				return
			}
			g.core.StopTunnel(alias)
			g.refresh()
		})
		r.spin = widget.NewActivity()
		r.spin.Hide()
		setup := widget.NewButton(g.t("setup_server"), func() { g.showSetupServer(alias) })
		rows = append(rows, r.status,
			container.NewHBox(r.toggle, r.spin, vscode, cli, setup, usageBtn, layout.NewSpacer(), edit, del))
	} else {
		// A plain ssh host: just show it and offer to make it a tunnel host.
		plain := widget.NewLabel(g.t("plain_host"))
		plain.Importance = widget.LowImportance
		rows = append(rows, plain,
			container.NewHBox(
				vscode, cli,
				widget.NewButton(g.t("enable_reverse"), func() {
					g.do(func() error { return g.core.SetReverseTunnel(alias, 2222) })
				}),
				usageBtn, layout.NewSpacer(), edit, del))
	}

	// Fill contrast only — no hairline stroke. A card's height is driven by text
	// metrics and lands on a fraction (~128.8px), so consecutive cards rasterise
	// at different subpixel phases. The layout gap is exactly uniform, but a 1px
	// stroke on each edge turns that ±1px of rounding into two borders that
	// sometimes touch and sometimes leave a visible sliver of background — which
	// reads as wildly uneven spacing, especially once a HiDPI scale doubles it.
	// Separating by fill alone is immune: there is no hard line to misalign.
	bg := canvas.NewRectangle(cardColor(g.app))
	bg.CornerRadius = 8
	r.root = container.NewStack(bg, container.NewPadded(container.NewVBox(rows...)))
	r.update(g, h)
	return r
}

// update re-syncs the parts of a card that track live state.
func (r *hostRow) update(g *gui, h core.HostView) {
	r.cur = h
	setLabel(r.title, h.Alias)
	if r.status != nil {
		setLabel(r.status, fmt.Sprintf(g.t("reverse_status_fmt"),
			h.ReversePort, g.stateLabel(h.Status)))
	}
	if r.toggle != nil {
		switch h.Status.State {
		case bridge.StateStopped:
			setButtonLook(r.toggle, g.t("start"), theme.MediaPlayIcon(), widget.HighImportance)
			stopSpinner(r.spin)
		case bridge.StateUp:
			setButtonLook(r.toggle, g.t("stop"), theme.MediaStopIcon(), widget.MediumImportance)
			stopSpinner(r.spin)
		default: // connecting, retrying — on its way somewhere, and interruptible
			setButtonLook(r.toggle, g.stateName(h.Status.State), theme.MediaStopIcon(), widget.MediumImportance)
			startSpinner(r.spin)
		}
	}
	g.setDot(r.dot, h)
}

// setDot paints the state marker: green when the tunnel is up, amber while it is
// coming up or retrying, neutral otherwise. A plain ssh host has no tunnel to
// report on, so it stays neutral too.
func (g *gui) setDot(c *canvas.Circle, h core.HostView) {
	want := dimColor(g.app)
	if h.HasReverse {
		switch h.Status.State {
		case bridge.StateUp:
			want = upColor(g.app)
		case bridge.StateConnecting, bridge.StateRetrying:
			want = pendingColor(g.app)
		}
	}
	if c.FillColor != want {
		c.FillColor = want
		c.Refresh()
	}
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

// ---- host dialogs ----

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

// showEdit is the per-host settings dialog. Besides the ssh identity it carries
// the two per-host switches — routing through the proxy, and starting this
// host's tunnel on launch — which used to sit on the card. They are settings, not
// controls: they belong behind Edit, not in the row you scan for status.
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
	useProxy := widget.NewCheck("", nil)
	useProxy.SetChecked(h.HasProxy)
	autoStart := widget.NewCheck("", nil)
	autoStart.SetChecked(h.AutoStart)

	items := []*widget.FormItem{
		widget.NewFormItem(g.t("host_ip"), host),
		widget.NewFormItem(g.t("ssh_user"), user),
		widget.NewFormItem(g.t("ssh_port"), port),
		widget.NewFormItem(g.t("reverse_port"), rport),
		widget.NewFormItem(g.t("use_proxy"), useProxy),
		widget.NewFormItem(g.t("auto_start_tunnel"), autoStart),
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
			if err := g.core.SetProxy(alias, useProxy.Checked); err != nil {
				return err
			}
			// Reverse tunnel is app metadata, not ssh config: blank/0 turns it off.
			if err := g.core.SetReverseTunnel(alias, atoi(rport.Text, 0)); err != nil {
				return err
			}
			// Auto-start is meaningless without a reverse tunnel, and SetAutoStart
			// is applied after SetReverseTunnel so turning the tunnel off here also
			// clears the flag rather than leaving it armed for a port that is gone.
			return g.core.SetAutoStart(alias, autoStart.Checked && atoi(rport.Text, 0) > 0)
		})
	}, g.win)
	d.Resize(fyne.NewSize(480, 420))
	d.Show()
}

// showSetupServer connects with key/agent auth only (never a password). If the
// server hasn't authorized this machine's key yet, it shows the public key for
// the user to add to the server, then re-run setup. The ssh work runs off the UI
// thread so the window stays responsive.
func (g *gui) showSetupServer(alias string) {
	prog := dialog.NewCustom(g.t("setup_server"), g.t("close"),
		waiting(fmt.Sprintf(g.t("connecting_fmt"), alias)), g.win)
	prog.Show()
	go func() {
		res, err := g.core.SetupServer(alias)
		fyne.Do(func() {
			prog.Hide()
			switch {
			case err == nil:
				g.setupDone(res)
			case isAuthError(err):
				g.showAuthorizeKey(alias) // key not authorized on the server yet
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

// showAuthorizeKey displays this machine's public key so the user can add it to
// the server's authorized_keys, then re-run "Set up server".
func (g *gui) showAuthorizeKey(alias string) {
	g.showPublicKey(g.t("authorize_title"), fmt.Sprintf(g.t("authorize_instr"), alias))
}

// showPublicKey is the shared public-key panel: the reactive one above, and the
// one reachable any time from Settings.
func (g *gui) showPublicKey(title, intro string) {
	pub, err := g.core.PublicKey()
	if err != nil {
		dialog.ShowError(err, g.win)
		return
	}
	info := widget.NewLabel(intro)
	info.Wrapping = fyne.TextWrapWord
	key := widget.NewMultiLineEntry()
	key.SetText(pub)
	key.Wrapping = fyne.TextWrapBreak
	key.Disable() // read-only; copy via the button
	copyBtn := widget.NewButtonWithIcon(g.t("copy"), theme.ContentCopyIcon(), func() {
		g.app.Clipboard().SetContent(pub)
		setLabel(g.status, g.t("copied"))
	})
	copyBtn.Importance = widget.HighImportance
	content := container.NewBorder(info, copyBtn, nil, nil, key)
	d := dialog.NewCustom(title, g.t("close"), content, g.win)
	d.Resize(fyne.NewSize(560, 340))
	d.Show()
}

func (g *gui) setupDone(res provision.ServerResult) {
	dialog.ShowInformation(g.t("server_configured"),
		fmt.Sprintf(g.t("server_conf_fmt"), res.Alias, g.authLabel(res.Authorized)), g.win)
	g.refresh()
}
