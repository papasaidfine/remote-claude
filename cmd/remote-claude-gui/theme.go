//go:build gui

package main

import (
	_ "embed"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// The UI font is Noto Sans SC (SIL OFL 1.1, see uifont.LICENSE.txt), subset to
// the characters this app's UI actually shows plus the ASCII/Latin-1/punctuation
// ranges, so the bundled files stay ~100KB each. Regenerate after adding new
// Chinese UI strings:
//
//	R="U+0020-007E,U+00A0-00FF,U+2000-206F,U+3000-303F,U+FF00-FFEF"
//	pyftsubset NotoSansSC-Regular.otf --text-file=internal/i18n/i18n.go \
//	  --unicodes=$R --output-file=cmd/remote-claude-gui/uifont.otf
//	pyftsubset NotoSansSC-Bold.otf    --text-file=internal/i18n/i18n.go \
//	  --unicodes=$R --output-file=cmd/remote-claude-gui/uifont-bold.otf

//go:embed uifont.otf
var uiFontRegularData []byte

//go:embed uifont-bold.otf
var uiFontBoldData []byte

var (
	uiFontRegular = fyne.NewStaticResource("uifont.otf", uiFontRegularData)
	uiFontBold    = fyne.NewStaticResource("uifont-bold.otf", uiFontBoldData)
)

// cjkTheme is Fyne's default theme with a CJK-capable font substituted for every
// text style. Using one font for both Latin and CJK removes Fyne's per-glyph
// fallback — the thing that made Chinese jitter above and below the baseline.
// Bold keeps its own weight; italic and monospace use the regular CJK face (the
// usage table no longer relies on a monospace grid for alignment).
type cjkTheme struct{ fyne.Theme }

func newCJKTheme() fyne.Theme { return &cjkTheme{Theme: theme.DefaultTheme()} }

func (t *cjkTheme) Font(s fyne.TextStyle) fyne.Resource {
	if s.Bold {
		return uiFontBold
	}
	return uiFontRegular
}
