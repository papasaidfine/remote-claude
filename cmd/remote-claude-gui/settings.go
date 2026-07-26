//go:build gui

package main

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/papasaidfine/remote-claude/internal/autostart"
	"github.com/papasaidfine/remote-claude/internal/core"
	"github.com/papasaidfine/remote-claude/internal/i18n"
)

// buildSettings lays out the Settings tab. Everything here is per-machine setup
// you touch rarely — which is exactly why it is off the Hosts tab, where the
// things you touch often live.
func (g *gui) buildSettings() fyne.CanvasObject {
	body := container.NewVBox(
		g.sectionGeneral(),
		g.sectionProxy(),
		g.sectionLocalSSHD(),
		g.sectionKeys(),
		g.sectionAbout(),
	)
	return container.NewPadded(container.NewVScroll(body))
}

// section renders one titled group with a rule under the heading.
func (g *gui) section(title string, body ...fyne.CanvasObject) fyne.CanvasObject {
	head := widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	rows := append([]fyne.CanvasObject{head, widget.NewSeparator()}, body...)
	rows = append(rows, widget.NewLabel("")) // breathing room before the next group
	return container.NewVBox(rows...)
}

// hint is small print under a control.
func hint(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.Wrapping = fyne.TextWrapWord
	l.Importance = widget.LowImportance
	return l
}

func (g *gui) sectionGeneral() fyne.CanvasObject {
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

	return g.section(g.t("settings_general"),
		container.NewHBox(widget.NewLabel(g.t("language")), langSel),
		autoLaunch,
	)
}

// sectionProxy holds what used to be the "Xray" dialog: fetching the components
// and editing the node list. The implementation is never named here — see the
// note on i18n's catalog.
func (g *gui) sectionProxy() fyne.CanvasObject {
	g.proxyState = widget.NewLabel("")

	via := widget.NewEntry()
	via.SetPlaceHolder(g.t("proxy_via_ph"))
	msg := widget.NewLabel("")
	msg.Importance = widget.LowImportance

	var install *widget.Button
	install = widget.NewButton(g.t("proxy_install"), func() {
		install.Disable()
		setLabel(msg, g.t("downloading"))
		p := strings.TrimSpace(via.Text)
		go func() {
			err := g.core.InstallXray(p)
			fyne.Do(func() {
				install.Enable()
				if err != nil {
					setLabel(msg, fmt.Sprintf(g.t("failed_fmt"), err.Error()))
					return
				}
				setLabel(msg, g.t("proxy_ready"))
				via.SetText("")
				g.refresh()
			})
		}()
	})

	nodes := widget.NewMultiLineEntry()
	raw, _ := g.core.Nodes()
	nodes.SetText(raw)
	nodes.SetPlaceHolder(g.t("nodes_ph"))
	nodes.Wrapping = fyne.TextWrapOff
	nodes.SetMinRowsVisible(8)

	save := widget.NewButton(g.t("save_nodes"), func() {
		n, err := g.core.SetNodes(nodes.Text)
		if err != nil {
			dialog.ShowError(err, g.win)
			return
		}
		setLabel(msg, fmt.Sprintf(g.t("nodes_saved_fmt"), n))
		g.refresh()
	})
	save.Importance = widget.HighImportance

	return g.section(g.t("settings_proxy"),
		hint(g.t("proxy_intro")),
		container.NewHBox(install, g.proxyState),
		container.NewBorder(nil, nil, nil, nil, via),
		msg,
		widget.NewLabel(g.t("nodes_label")),
		nodes,
		container.NewHBox(layout.NewSpacer(), save),
	)
}

func (g *gui) sectionLocalSSHD() fyne.CanvasObject {
	g.sshdState = widget.NewLabel("")

	disablePw := widget.NewCheck(g.t("sshd_disable_pw"), nil)
	disablePw.SetChecked(true)
	install := widget.NewButton(g.t("sshd_install"), func() {
		running, err := g.core.EnsureLocalSSHD(disablePw.Checked)
		if err != nil {
			dialog.ShowError(err, g.win)
			return
		}
		dialog.ShowInformation(g.t("local_ssh_server"),
			fmt.Sprintf(g.t("sshd_done_fmt"), g.yn(running)), g.win)
		g.refresh()
	})

	return g.section(g.t("settings_local_ssh"),
		hint(g.t("sshd_info")),
		disablePw,
		container.NewHBox(install, g.sshdState),
	)
}

// sectionKeys is a front door to the machine's public key. It used to appear
// only reactively, when setting up a server failed on authentication — which is
// no help when you want to authorize this machine ahead of time.
func (g *gui) sectionKeys() fyne.CanvasObject {
	show := widget.NewButtonWithIcon(g.t("key_show"), theme.VisibilityIcon(), func() {
		g.showPublicKey(g.t("key_title"), g.t("key_intro"))
	})
	return g.section(g.t("settings_keys"),
		hint(g.t("key_intro")),
		container.NewHBox(show),
	)
}

func (g *gui) sectionAbout() fyne.CanvasObject {
	check := widget.NewButtonWithIcon(g.t("check_update"), theme.DownloadIcon(), g.checkUpdate)
	return g.section(g.t("settings_about"),
		container.NewHBox(
			widget.NewLabel(fmt.Sprintf(g.t("version_fmt"), version)),
			layout.NewSpacer(),
			check,
		),
	)
}

// refreshSettings re-syncs the live readouts on the Settings tab.
func (g *gui) refreshSettings(st core.State) {
	if g.proxyState == nil { // Settings tab not built yet
		return
	}
	switch {
	case !st.XraySupported:
		setLabel(g.proxyState, g.t("proxy_unsupported"))
	case st.XrayInstalled:
		setLabel(g.proxyState, g.t("proxy_installed"))
	default:
		setLabel(g.proxyState, g.t("proxy_missing"))
	}
	setLabel(g.sshdState, g.yn(st.LocalSSHOK))
}
