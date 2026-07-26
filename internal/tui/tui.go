// Package tui is the terminal front-end. It drives the same core.App the
// desktop GUI does, and shares its message catalog (internal/i18n) so the two
// interfaces cannot drift apart in wording.
//
// It exists for machines the GUI cannot run on — a headless server, anything
// reached over plain ssh — where the alternative would be hand-editing
// ~/.ssh/config.
package tui

import (
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/papasaidfine/remote-claude/internal/bridge"
	"github.com/papasaidfine/remote-claude/internal/core"
	"github.com/papasaidfine/remote-claude/internal/i18n"
)

// Page names in the root Pages primitive.
const (
	pageHosts    = "hosts"
	pageSettings = "settings"
	pageModal    = "modal"
)

// UI is the terminal application.
type UI struct {
	app     *tview.Application
	core    *core.App
	lang    i18n.Lang
	pr      i18n.Printer
	version string

	pages  *tview.Pages
	tabs   *tview.TextView
	table  *tview.Table
	status *tview.TextView
	keys   *tview.TextView

	hosts []core.HostView // what the table currently shows, in row order

	// settings widgets whose contents track live state
	settings   *tview.Form
	proxyState *tview.TextView
	sshdState  *tview.TextView
	nodesDraft string
	proxyVia   string
	disablePw  bool
}

// New builds the terminal UI over app.
func New(app *core.App, lang i18n.Lang, version string) *UI {
	// Inherit the terminal's own background instead of painting over it: this
	// runs inside whatever colour scheme the user already chose.
	tview.Styles.PrimitiveBackgroundColor = tcell.ColorDefault
	tview.Styles.ContrastBackgroundColor = tcell.ColorDarkSlateGray
	tview.Styles.PrimaryTextColor = tcell.ColorDefault
	tview.Styles.SecondaryTextColor = tcell.ColorDarkOrange
	tview.Styles.TertiaryTextColor = tcell.ColorGreen
	tview.Styles.BorderColor = tcell.ColorGray
	tview.Styles.TitleColor = tcell.ColorDefault

	u := &UI{
		app:       tview.NewApplication(),
		core:      app,
		lang:      lang,
		pr:        i18n.P(lang),
		version:   version,
		disablePw: true,
	}
	u.build()
	return u
}

func (u *UI) t(key string) string { return u.pr.T(key) }

// build assembles the widget tree: a header, the page body, and two status rows.
func (u *UI) build() {
	u.tabs = tview.NewTextView().SetDynamicColors(true)
	u.status = tview.NewTextView().SetDynamicColors(true)
	u.keys = tview.NewTextView().SetDynamicColors(true)

	u.pages = tview.NewPages().
		AddPage(pageHosts, u.buildHosts(), true, true).
		AddPage(pageSettings, u.buildSettings(), true, false)

	root := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(u.tabs, 1, 0, false).
		AddItem(u.pages, 0, 1, true).
		AddItem(u.status, 1, 0, false).
		AddItem(u.keys, 1, 0, false)

	u.app.SetRoot(root, true).SetInputCapture(u.globalKeys)
	u.showPage(pageHosts)
}

// globalKeys handles the page switches and quit. It stands aside whenever a
// field has the keyboard — otherwise typing "1" into the proxy URL would jump
// to another page instead of entering a character.
func (u *UI) globalKeys(ev *tcell.EventKey) *tcell.EventKey {
	if u.pages.HasPage(pageModal) || u.typing() {
		return ev
	}
	switch ev.Rune() {
	case '1':
		u.showPage(pageHosts)
		return nil
	case '2':
		u.showPage(pageSettings)
		return nil
	case 'q':
		u.app.Stop()
		return nil
	}
	return ev
}

// typing reports whether the focused widget consumes plain characters.
func (u *UI) typing() bool {
	switch u.app.GetFocus().(type) {
	case *tview.InputField, *tview.TextArea:
		return true
	}
	return false
}

func (u *UI) showPage(name string) {
	u.pages.SwitchToPage(name)
	switch name {
	case pageHosts:
		u.app.SetFocus(u.table)
		u.keys.SetText(u.t("keys_hosts"))
	case pageSettings:
		u.app.SetFocus(u.settings)
		u.keys.SetText(u.t("keys_settings"))
	}
	u.drawTabs(name)
}

func (u *UI) drawTabs(active string) {
	mark := func(page, key, label string) string {
		if page == active {
			return fmt.Sprintf("[black:darkorange] %s %s [-:-:-]", key, label)
		}
		return fmt.Sprintf(" [darkorange]%s[-] %s ", key, label)
	}
	u.tabs.SetText(fmt.Sprintf("%s%s   [gray]remote-claude %s[-]",
		mark(pageHosts, "1", u.t("tab_hosts")),
		mark(pageSettings, "2", u.t("tab_settings")),
		u.version))
}

// Run starts the event loop and keeps the display current until the user quits.
func (u *UI) Run() error {
	u.refresh()
	go u.autoRefresh()
	return u.app.Run()
}

// Stop ends the event loop.
func (u *UI) Stop() { u.app.Stop() }

// autoRefresh re-reads tunnel status on a timer. Widget mutations must happen on
// the draw goroutine, so it goes through QueueUpdateDraw.
func (u *UI) autoRefresh() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for range t.C {
		u.app.QueueUpdateDraw(u.refresh)
	}
}

// refresh pulls a fresh snapshot into every live widget.
func (u *UI) refresh() {
	st := u.core.State()
	u.fillHosts(st.Hosts)
	u.status.SetText(fmt.Sprintf(u.t("status_fmt"),
		st.Platform, u.yn(st.LocalSSHOK), st.NodeCount))
	u.refreshSettings(st)
}

// ---- hosts page ----

func (u *UI) buildHosts() tview.Primitive {
	u.table = tview.NewTable().
		SetSelectable(true, false).
		SetFixed(1, 0)
	u.table.SetInputCapture(u.hostKeys)
	return u.table
}

// fillHosts rebuilds the table, preserving the cursor. The ticker redraws every
// couple of seconds; without the save/restore the selection would walk back to
// the top under the user's hands, and the selection is what every host key acts on.
func (u *UI) fillHosts(hosts []core.HostView) {
	selected, _ := u.table.GetSelection()
	u.hosts = hosts
	u.table.Clear()

	for col, title := range []string{u.t("col_alias"), u.t("col_target"), u.t("col_tunnel"), u.t("col_state")} {
		u.table.SetCell(0, col, tview.NewTableCell(title).
			SetTextColor(tcell.ColorDarkOrange).
			SetAttributes(tcell.AttrBold).
			SetSelectable(false).
			SetExpansion(colExpansion(col)))
	}

	if len(hosts) == 0 {
		u.table.SetCell(1, 0, tview.NewTableCell(u.t("no_hosts")).
			SetSelectable(false).
			SetExpansion(1))
		return
	}

	for i, h := range hosts {
		row := i + 1
		u.table.SetCell(row, 0, tview.NewTableCell(h.Alias).SetAttributes(tcell.AttrBold))
		u.table.SetCell(row, 1, tview.NewTableCell(target(h)).SetExpansion(1))
		u.table.SetCell(row, 2, tview.NewTableCell(tunnelCell(h)))
		u.table.SetCell(row, 3, tview.NewTableCell(u.stateCell(h)).SetTextColor(stateColor(h)))
	}

	if selected < 1 {
		selected = 1
	}
	if selected > len(hosts) {
		selected = len(hosts)
	}
	u.table.Select(selected, 0)
}

func colExpansion(col int) int {
	if col == 1 {
		return 1
	}
	return 0
}

func tunnelCell(h core.HostView) string {
	if !h.HasReverse {
		return "—"
	}
	return fmt.Sprintf(":%d", h.ReversePort)
}

// stateCell is the tunnel state with a marker. Unlike the desktop window, whose
// bundled font is subset and has no "●", a terminal renders it from the user's
// own font.
func (u *UI) stateCell(h core.HostView) string {
	if !h.HasReverse {
		return u.t("plain_host")
	}
	return "● " + u.stateName(h.Status.State)
}

func stateColor(h core.HostView) tcell.Color {
	if !h.HasReverse {
		return tcell.ColorGray
	}
	switch h.Status.State {
	case bridge.StateUp:
		return tcell.ColorGreen
	case bridge.StateConnecting, bridge.StateRetrying:
		return tcell.ColorDarkOrange
	}
	return tcell.ColorGray
}

// selectedHost is the host the cursor is on, or nil when the list is empty.
func (u *UI) selectedHost() *core.HostView {
	if len(u.hosts) == 0 {
		return nil
	}
	row, _ := u.table.GetSelection()
	i := row - 1 // row 0 is the header
	if i < 0 || i >= len(u.hosts) {
		return nil
	}
	return &u.hosts[i]
}

// hostKeys are the per-host actions, live only while the table has focus.
func (u *UI) hostKeys(ev *tcell.EventKey) *tcell.EventKey {
	if ev.Rune() == 'a' {
		u.showAddHost()
		return nil
	}
	if ev.Rune() == 'r' {
		u.refresh()
		return nil
	}
	h := u.selectedHost()
	if h == nil {
		return ev
	}
	switch ev.Rune() {
	case 's':
		u.act(func() error { _, err := u.core.StartTunnel(h.Alias); return err })
	case 'x':
		u.core.StopTunnel(h.Alias)
		u.refresh()
	case 'e':
		u.showEdit(*h)
	case 'u':
		u.showUsage(h.Alias)
	case 'c':
		u.showSetupServer(h.Alias)
	case 'd':
		u.confirmDelete(h.Alias)
	default:
		return ev
	}
	return nil
}

// act runs a mutating call, reporting any failure and refreshing on success.
func (u *UI) act(fn func() error) {
	if err := fn(); err != nil {
		u.showError(err)
		return
	}
	u.refresh()
}

// ---- shared helpers ----

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

func (u *UI) stateName(st bridge.State) string {
	switch st {
	case bridge.StateStopped:
		return u.t("state_stopped")
	case bridge.StateConnecting:
		return u.t("state_connecting")
	case bridge.StateUp:
		return u.t("state_up")
	case bridge.StateRetrying:
		return u.t("state_retrying")
	}
	return string(st)
}

func (u *UI) yn(b bool) string {
	if b {
		return u.t("running")
	}
	return u.t("not_detected")
}

func (u *UI) authLabel(b bool) string {
	if b {
		return u.t("authorized")
	}
	return u.t("already_present")
}
