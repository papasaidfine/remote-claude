package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/papasaidfine/remote-claude/internal/core"
	"github.com/papasaidfine/remote-claude/internal/provision"
	"github.com/papasaidfine/remote-claude/internal/usage"
)

// modal floats p over the current page at a fixed size.
func (u *UI) modal(p tview.Primitive, width, height int) {
	centred := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(p, height, 0, true).
			AddItem(nil, 0, 1, false), width, 0, true).
		AddItem(nil, 0, 1, false)
	u.pages.AddPage(pageModal, centred, true, true)
	u.app.SetFocus(p)
}

// closeModal dismisses the floating panel and hands the keyboard back.
func (u *UI) closeModal() {
	u.pages.RemovePage(pageModal)
	if name, _ := u.pages.GetFrontPage(); name == pageSettings {
		u.app.SetFocus(u.settings)
		return
	}
	u.app.SetFocus(u.table)
}

// showBusy puts up an uncancellable notice for a blocking call and returns the
// function that takes it down again.
func (u *UI) showBusy(msg string) func() {
	v := tview.NewTextView().SetText("\n  " + msg).SetTextAlign(tview.AlignCenter)
	v.SetBorder(true)
	u.modal(v, 60, 5)
	return u.closeModal
}

func (u *UI) showError(err error) {
	u.showInfo("", fmt.Sprintf(u.t("failed_fmt"), err.Error()))
}

// showInfo is a dismissable message box. The title goes into the body rather
// than onto the Box: tview.Modal draws through an internal Frame, so a title set
// on the widget itself never appears.
func (u *UI) showInfo(title, msg string) {
	if title != "" {
		msg = title + "\n\n" + msg
	}
	m := tview.NewModal().
		SetText(msg).
		AddButtons([]string{u.t("close")}).
		SetDoneFunc(func(int, string) { u.closeModal() })
	u.pages.AddPage(pageModal, m, true, true)
	u.app.SetFocus(m)
}

func (u *UI) confirmDelete(alias string) {
	m := tview.NewModal().
		SetText(fmt.Sprintf(u.t("delete_host_conf_fmt"), alias)).
		AddButtons([]string{u.t("cancel"), u.t("delete")}).
		SetDoneFunc(func(i int, _ string) {
			u.closeModal()
			if i == 1 {
				u.act(func() error { return u.core.RemoveHost(alias) })
			}
		})
	u.pages.AddPage(pageModal, m, true, true)
	u.app.SetFocus(m)
}

func (u *UI) showAddHost() {
	form := tview.NewForm()
	form.AddInputField(u.t("alias_ssh_name"), "", 30, nil, nil)
	form.AddInputField(u.t("host_ip"), "", 30, nil, nil)
	form.AddInputField(u.t("ssh_user"), "", 30, nil, nil)
	form.AddInputField(u.t("ssh_port"), "22", 8, nil, nil)
	form.AddButton(u.t("add"), func() {
		alias := fieldText(form, u.t("alias_ssh_name"))
		host := fieldText(form, u.t("host_ip"))
		user := fieldText(form, u.t("ssh_user"))
		port := atoi(fieldText(form, u.t("ssh_port")), 22)
		u.closeModal()
		u.act(func() error { return u.core.AddHost(alias, host, user, port) })
	})
	form.AddButton(u.t("cancel"), u.closeModal)
	form.SetBorder(true).SetTitle(" " + u.t("add_host_title") + " ")
	u.modal(form, 62, 13)
}

// showEdit is the per-host settings panel. Besides the ssh identity it carries
// the two per-host switches — routing through the proxy, and starting this
// host's tunnel on launch. They are settings, not controls, so they live here
// rather than in the row you scan for status.
func (u *UI) showEdit(h core.HostView) {
	rport := ""
	if h.ReversePort > 0 {
		rport = strconv.Itoa(h.ReversePort)
	}
	form := tview.NewForm()
	form.AddInputField(u.t("host_ip"), h.HostName, 30, nil, nil)
	form.AddInputField(u.t("ssh_user"), h.User, 30, nil, nil)
	form.AddInputField(u.t("ssh_port"), h.Port, 8, nil, nil)
	form.AddInputField(u.t("reverse_port"), rport, 8, nil, nil)
	form.AddCheckbox(u.t("use_proxy"), h.HasProxy, nil)
	form.AddCheckbox(u.t("auto_start_tunnel"), h.AutoStart, nil)

	alias := h.Alias
	form.AddButton(u.t("save"), func() {
		host := fieldText(form, u.t("host_ip"))
		user := fieldText(form, u.t("ssh_user"))
		port := fieldText(form, u.t("ssh_port"))
		reverse := atoi(fieldText(form, u.t("reverse_port")), 0)
		useProxy := checked(form, u.t("use_proxy"))
		autoStart := checked(form, u.t("auto_start_tunnel"))
		u.closeModal()
		u.act(func() error {
			if err := u.core.SetParam(alias, "HostName", host); err != nil {
				return err
			}
			if err := u.core.SetParam(alias, "User", user); err != nil {
				return err
			}
			if err := u.core.SetParam(alias, "Port", port); err != nil {
				return err
			}
			if err := u.core.SetProxy(alias, useProxy); err != nil {
				return err
			}
			// Reverse tunnel is app metadata, not ssh config: blank/0 turns it off.
			if err := u.core.SetReverseTunnel(alias, reverse); err != nil {
				return err
			}
			// Auto-start is meaningless without a reverse tunnel, so turning the
			// tunnel off here also disarms the flag rather than leaving it set
			// for a port that no longer exists.
			return u.core.SetAutoStart(alias, autoStart && reverse > 0)
		})
	})
	form.AddButton(u.t("cancel"), u.closeModal)
	form.SetBorder(true).SetTitle(" " + fmt.Sprintf(u.t("edit_title_fmt"), alias) + " ")
	u.modal(form, 62, 17)
}

// showSetupServer bootstraps the far end over ssh, using key/agent auth only. If
// this machine's key is not authorized there yet, the public key comes up for
// the user to install before retrying.
func (u *UI) showSetupServer(alias string) {
	done := u.showBusy(fmt.Sprintf(u.t("connecting_fmt"), alias))
	go func() {
		res, err := u.core.SetupServer(alias)
		u.app.QueueUpdateDraw(func() {
			done()
			switch {
			case err == nil:
				u.setupDone(res)
			case isAuthError(err):
				u.showPublicKey(u.t("authorize_title"), fmt.Sprintf(u.t("authorize_instr"), alias))
			default:
				u.showError(err)
			}
		})
	}()
}

func (u *UI) setupDone(res provision.ServerResult) {
	u.showInfo(u.t("server_configured"),
		fmt.Sprintf(u.t("server_conf_fmt"), res.Alias, u.authLabel(res.Authorized)))
	u.refresh()
}

// isAuthError reports whether an ssh failure is a public-key rejection (vs. a
// connection error, a server-side script failure, etc.).
func isAuthError(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "permission denied") || strings.Contains(s, "publickey")
}

// showPublicKey displays this machine's public key. There is no clipboard to
// copy to over ssh, so the key is shown selectable for the terminal's own copy.
func (u *UI) showPublicKey(title, intro string) {
	pub, err := u.core.PublicKey()
	if err != nil {
		u.showError(err)
		return
	}
	v := tview.NewTextView().SetWrap(true).SetText(intro + "\n\n" + pub)
	v.SetBorder(true).SetTitle(" " + title + " ")
	v.SetDoneFunc(func(tcell.Key) { u.closeModal() })
	u.modal(v, 76, 14)
}

// showUsage reads Claude usage from the host over ssh and shows the priced
// 1D/7D/30D breakdown as a real table — column widths are computed from the
// content, which space-padding cannot do once CJK glyphs are involved.
func (u *UI) showUsage(alias string) {
	done := u.showBusy(fmt.Sprintf(u.t("reading_usage_fmt"), alias))
	go func() {
		rep, err := u.core.HostUsage(alias)
		u.app.QueueUpdateDraw(func() {
			done()
			if err != nil {
				u.showError(err)
				return
			}
			t := u.usageTable(rep)
			t.SetBorder(true).SetTitle(" " + fmt.Sprintf(u.t("usage_title_fmt"), alias) + " ")
			t.SetDoneFunc(func(tcell.Key) { u.closeModal() })
			u.modal(t, 100, 30)
		})
	}()
}

// barCells is how wide the share-of-cost bar is drawn, in terminal cells.
const barCells = 22

// usageTable lays each window out as a headline total, then one row per model
// carrying a bar for its share of the cost alongside the raw numbers.
//
// The bar is the point: cost per model is a magnitude comparison, and a length
// answers "which model is eating the budget" at a glance where a column of
// dollar figures has to be read and compared. It stays in a Table so the
// numbers keep their alignment — which space-padding cannot do once the labels
// are Chinese.
//
// One hue for every bar, deliberately: they all encode the same measure, so a
// colour per model would imply a distinction that isn't there.
func (u *UI) usageTable(rep usage.Report) *tview.Table {
	t := tview.NewTable().SetSelectable(false, false)
	row := 0
	cell := func(col int, text string, colour tcell.Color, align int, bold bool) {
		c := tview.NewTableCell(text).SetTextColor(colour).SetAlign(align)
		if bold {
			c.SetAttributes(tcell.AttrBold)
		}
		t.SetCell(row, col, c)
	}
	numbers := func(tk usage.Tokens, cost float64, bold bool) {
		cell(2, "$"+money(cost), tcell.ColorDefault, tview.AlignRight, bold)
		cell(3, tok(tk.Input), tcell.ColorGray, tview.AlignRight, bold)
		cell(4, tok(tk.Output), tcell.ColorGray, tview.AlignRight, bold)
		cell(5, tok(tk.CacheWrite), tcell.ColorGray, tview.AlignRight, bold)
		cell(6, tok(tk.CacheRead), tcell.ColorGray, tview.AlignRight, bold)
	}
	section := func(title string, w usage.Window) {
		cell(0, title, tcell.ColorDarkOrange, tview.AlignLeft, true)
		if len(w.Models) == 0 {
			row++
			cell(0, u.t("no_usage_window"), tcell.ColorGray, tview.AlignLeft, false)
			row += 2
			return
		}
		// Headline first: the total is the number you came for.
		cell(2, fmt.Sprintf(u.t("usage_total_fmt"), "$"+money(w.Cost)),
			tcell.ColorDarkOrange, tview.AlignRight, true)
		row++

		for i, h := range []string{u.t("col_model"), u.t("col_share"), u.t("col_cost"),
			u.t("col_input"), u.t("col_output"), u.t("col_cache_w"), u.t("col_cache_r")} {
			align := tview.AlignRight
			if i < 2 {
				align = tview.AlignLeft
			}
			cell(i, h, tcell.ColorGray, align, false)
		}
		row++

		peak := 0.0
		for _, m := range w.Models {
			if m.Cost > peak {
				peak = m.Cost
			}
		}
		for _, m := range w.Models {
			frac := 0.0
			if peak > 0 {
				frac = m.Cost / peak
			}
			cell(0, shortModel(m.Model), tcell.ColorDefault, tview.AlignLeft, false)
			cell(1, bar(frac, barCells), tcell.ColorDarkOrange, tview.AlignLeft, false)
			numbers(m.Tokens, m.Cost, false)
			row++
		}
		cell(0, u.t("col_total"), tcell.ColorDefault, tview.AlignLeft, true)
		numbers(w.Total, w.Cost, true)
		row += 2 // blank line between sections
	}
	section(u.t("past_1d"), rep.Day)
	section(u.t("past_7d"), rep.Week)
	section(u.t("past_30d"), rep.Month)
	return t
}

// ---- form readers ----

func fieldText(f *tview.Form, label string) string {
	if item := f.GetFormItemByLabel(label); item != nil {
		if in, ok := item.(*tview.InputField); ok {
			return strings.TrimSpace(in.GetText())
		}
	}
	return ""
}

func checked(f *tview.Form, label string) bool {
	if item := f.GetFormItemByLabel(label); item != nil {
		if c, ok := item.(*tview.Checkbox); ok {
			return c.IsChecked()
		}
	}
	return false
}

// ---- formatting ----

func atoi(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return def
	}
	return n
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
