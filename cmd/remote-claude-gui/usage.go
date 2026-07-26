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
	"fyne.io/fyne/v2/widget"

	"github.com/papasaidfine/remote-claude/internal/usage"
)

// showUsage fetches Claude usage from the host over ssh (off the UI thread) and
// shows a 1D/7D/30D tabbed, priced breakdown.
func (g *gui) showUsage(alias string) {
	body := container.NewStack(waiting(fmt.Sprintf(g.t("reading_usage_fmt"), alias)))
	d := dialog.NewCustom(fmt.Sprintf(g.t("usage_title_fmt"), alias), g.t("close"), body, g.win)
	// Six columns of numbers plus a chart want room. Take what the window can
	// spare rather than a fixed guess that is cramped on a large display and
	// overflowing on a small one.
	d.Resize(dialogSize(g.win.Canvas().Size(), fyne.NewSize(680, 520)))
	d.Show()
	go func() {
		rep, err := g.core.HostUsage(alias)
		fyne.Do(func() {
			if err != nil {
				body.Objects = []fyne.CanvasObject{container.NewPadded(
					widget.NewLabel(fmt.Sprintf(g.t("failed_fmt"), err.Error())))}
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

// usageWindow renders one time window: the total spend as the headline, a bar
// per model for where it went, then the token detail.
//
// Cost per model is a magnitude comparison, and a length answers "what is eating
// the budget" at a glance where a column of dollar figures has to be read and
// compared first. Every bar is the same hue on purpose — they all encode the one
// measure, so a colour per model would imply a distinction that is not there.
func (g *gui) usageWindow(w usage.Window) fyne.CanvasObject {
	if len(w.Models) == 0 {
		return container.NewPadded(widget.NewLabel(g.t("no_usage_window")))
	}

	total := widget.NewLabelWithStyle(
		fmt.Sprintf(g.t("usage_total_fmt"), "$"+money(w.Cost)),
		fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	head := container.NewBorder(nil, nil, total, nil)

	costs := make([]float64, len(w.Models))
	for i, m := range w.Models {
		costs[i] = m.Cost
	}
	shares := costShares(costs)

	chart := container.NewVBox()
	for i, m := range w.Models {
		chart.Add(g.costBar(shortModel(m.Model), shares[i], m.Cost))
	}

	// Scrolls both ways, not just vertically. The grid cannot shrink below the
	// width of its own labels, so in a narrow window a vertical-only scroll
	// silently clipped the last column — the cost, which is the number the page
	// exists to show — with no way to reach it.
	return container.NewScroll(container.NewPadded(container.NewVBox(
		head,
		widget.NewSeparator(),
		chart,
		widget.NewSeparator(),
		g.usageGrid(w),
	)))
}

// dialogSize fits a dialog to the window it opens over: most of the available
// room, never below what the content needs to lay out without clipping.
func dialogSize(win, min fyne.Size) fyne.Size {
	const margin = 0.92
	w, h := win.Width*margin, win.Height*margin
	if w < min.Width {
		w = min.Width
	}
	if h < min.Height {
		h = min.Height
	}
	return fyne.NewSize(w, h)
}

// costBar is one row of the chart: the model, its bar, and what it cost.
func (g *gui) costBar(model string, share float32, cost float64) fyne.CanvasObject {
	name := widget.NewLabel(model)
	amount := widget.NewLabelWithStyle("$"+money(cost), fyne.TextAlignTrailing, fyne.TextStyle{})

	fill := canvas.NewRectangle(accentColor(g.app))
	fill.CornerRadius = 3 // rounded data-end, square against the baseline it grows from
	track := container.New(barLayout{frac: share}, fill)

	return container.NewBorder(nil, nil,
		container.New(layout.NewGridWrapLayout(fyne.NewSize(150, 30)), name),
		container.New(layout.NewGridWrapLayout(fyne.NewSize(80, 30)), amount),
		container.NewPadded(track))
}

// barLayout stretches its object across frac of the track's width.
type barLayout struct{ frac float32 }

// minBarWidth keeps a non-zero share visible. Rounding a sliver to nothing would
// say "this model was not used", which is a different claim from "barely used".
const minBarWidth = 3

func (b barLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.NewSize(60, 12) }

func (b barLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	frac := b.frac
	if frac > 1 {
		frac = 1
	}
	w := size.Width * frac
	if frac > 0 && w < minBarWidth {
		w = minBarWidth
	}
	if w < 0 {
		w = 0
	}
	for _, o := range objs {
		o.Resize(fyne.NewSize(w, size.Height))
		o.Move(fyne.NewPos(0, 0))
	}
}

// costShares scales each cost against the dearest one, so the chart's full width
// always means "the biggest line item here".
func costShares(costs []float64) []float32 {
	peak := 0.0
	for _, c := range costs {
		if c > peak {
			peak = c
		}
	}
	out := make([]float32, len(costs))
	if peak <= 0 {
		return out
	}
	for i, c := range costs {
		out[i] = float32(c / peak)
	}
	return out
}

// usageGrid is the token detail under the chart: a real 6-column grid, not a
// monospace ASCII table — full-width CJK glyphs and proportional fonts cannot be
// aligned by space-padding, so each cell is its own aligned label.
func (g *gui) usageGrid(w usage.Window) fyne.CanvasObject {
	var cells []fyne.CanvasObject
	cell := func(text string, align fyne.TextAlign, bold bool) {
		cells = append(cells, widget.NewLabelWithStyle(text, align, fyne.TextStyle{Bold: bold}))
	}
	row := func(name string, tk usage.Tokens, cost float64, bold bool) {
		cell(name, fyne.TextAlignLeading, bold)
		cell(tok(tk.Input), fyne.TextAlignTrailing, bold)
		cell(tok(tk.Output), fyne.TextAlignTrailing, bold)
		cell(tok(tk.CacheWrite), fyne.TextAlignTrailing, bold)
		cell(tok(tk.CacheRead), fyne.TextAlignTrailing, bold)
		cell("$"+money(cost), fyne.TextAlignTrailing, bold)
	}
	cell(g.t("col_model"), fyne.TextAlignLeading, true)
	cell(g.t("col_input"), fyne.TextAlignTrailing, true)
	cell(g.t("col_output"), fyne.TextAlignTrailing, true)
	cell(g.t("col_cache_w"), fyne.TextAlignTrailing, true)
	cell(g.t("col_cache_r"), fyne.TextAlignTrailing, true)
	cell(g.t("col_cost"), fyne.TextAlignTrailing, true)
	for _, m := range w.Models {
		row(shortModel(m.Model), m.Tokens, m.Cost, false)
	}
	row(g.t("col_total"), w.Total, w.Cost, true)
	return container.New(layout.NewGridLayoutWithColumns(6), cells...)
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
