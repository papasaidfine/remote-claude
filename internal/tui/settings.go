package tui

import (
	"fmt"
	"strings"

	"github.com/rivo/tview"

	"github.com/papasaidfine/remote-claude/internal/core"
	"github.com/papasaidfine/remote-claude/internal/i18n"
)

// buildSettings is the per-machine setup page: everything you touch rarely,
// kept off the hosts page where the things you touch often live.
//
// Headings are TextView rows inside the one Form rather than separate Forms in a
// Flex, so Tab still walks the whole page in order instead of stopping at each
// section boundary.
func (u *UI) buildSettings() tview.Primitive {
	u.proxyState = tview.NewTextView().SetDynamicColors(true)
	u.sshdState = tview.NewTextView().SetDynamicColors(true)

	raw, _ := u.core.Nodes()
	u.nodesDraft = raw

	form := tview.NewForm()

	// general
	form.AddTextView(u.t("settings_general"), "", 0, 1, false, false)
	var names []string
	current := 0
	for i, l := range i18n.Available {
		names = append(names, l.Name())
		if l == u.lang {
			current = i
		}
	}
	form.AddDropDown(u.t("language"), names, current, func(_ string, i int) {
		if i >= 0 && i < len(i18n.Available) {
			u.applyLang(i18n.Available[i])
		}
	})

	// proxy. The long "download via proxy, e.g. …" string is the field's
	// placeholder, not its label: tview sizes the label column to the widest
	// label, so using it there would shove every field on the page to the right.
	form.AddTextView(u.t("settings_proxy"), u.t("proxy_intro"), 0, 3, false, true)
	form.AddInputField(u.t("proxy_via_label"), "", 44, nil, func(s string) { u.proxyVia = s })
	if in, ok := form.GetFormItemByLabel(u.t("proxy_via_label")).(*tview.InputField); ok {
		in.SetPlaceholder(u.t("proxy_via_ph"))
	}
	form.AddButton(u.t("proxy_install"), u.installProxy)
	form.AddTextArea(u.t("nodes_label"), raw, 0, 6, 0, func(s string) { u.nodesDraft = s })
	form.AddButton(u.t("save_nodes"), u.saveNodes)

	// local ssh server
	form.AddTextView(u.t("settings_local_ssh"), u.t("sshd_info"), 0, 3, false, true)
	form.AddCheckbox(u.t("sshd_disable_pw"), u.disablePw, func(b bool) { u.disablePw = b })
	form.AddButton(u.t("sshd_install"), u.installSSHD)

	// ssh key
	form.AddTextView(u.t("settings_keys"), u.t("key_intro"), 0, 3, false, true)
	form.AddButton(u.t("key_show"), func() {
		u.showPublicKey(u.t("key_title"), u.t("key_intro"))
	})

	// about
	form.AddTextView(u.t("settings_about"), fmt.Sprintf(u.t("version_fmt"), u.version), 0, 1, false, false)

	u.settings = form
	return tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(form, 0, 1, true).
		AddItem(u.proxyState, 1, 0, false).
		AddItem(u.sshdState, 1, 0, false)
}

// applyLang switches the UI language, persists it, and rebuilds every caption.
func (u *UI) applyLang(l i18n.Lang) {
	if l == u.lang {
		return
	}
	u.lang = l
	u.pr = i18n.P(l)
	_ = u.core.SetLang(string(l))
	u.build()
	u.refresh()
	u.showPage(pageSettings)
}

// refreshSettings re-syncs the live readouts under the settings form.
func (u *UI) refreshSettings(st core.State) {
	if u.proxyState == nil { // not built yet
		return
	}
	var proxy string
	switch {
	case !st.XraySupported:
		proxy = u.t("proxy_unsupported")
	case st.XrayInstalled:
		proxy = "[green]" + u.t("proxy_installed") + "[-]"
	default:
		proxy = "[gray]" + u.t("proxy_missing") + "[-]"
	}
	u.proxyState.SetText(" " + u.t("settings_proxy") + ": " + proxy)
	u.sshdState.SetText(" " + u.t("settings_local_ssh") + ": " + u.yn(st.LocalSSHOK))
}

// installProxy fetches the proxy components, optionally through a one-shot http
// proxy. The download runs off the draw goroutine so the UI stays live.
func (u *UI) installProxy() {
	done := u.showBusy(u.t("downloading"))
	via := strings.TrimSpace(u.proxyVia)
	go func() {
		err := u.core.InstallXray(via)
		u.app.QueueUpdateDraw(func() {
			done()
			if err != nil {
				u.showError(err)
				return
			}
			u.showInfo(u.t("settings_proxy"), u.t("proxy_ready"))
			u.refresh()
		})
	}()
}

func (u *UI) saveNodes() {
	n, err := u.core.SetNodes(u.nodesDraft)
	if err != nil {
		u.showError(err)
		return
	}
	u.showInfo(u.t("settings_proxy"), fmt.Sprintf(u.t("nodes_saved_fmt"), n))
	u.refresh()
}

// installSSHD may prompt for elevation in the terminal this app was started
// from, so it runs off the draw goroutine like any other blocking call.
func (u *UI) installSSHD() {
	done := u.showBusy(u.t("sshd_install"))
	disable := u.disablePw
	go func() {
		running, err := u.core.EnsureLocalSSHD(disable)
		u.app.QueueUpdateDraw(func() {
			done()
			if err != nil {
				u.showError(err)
				return
			}
			u.showInfo(u.t("local_ssh_server"), fmt.Sprintf(u.t("sshd_done_fmt"), u.yn(running)))
			u.refresh()
		})
	}()
}
