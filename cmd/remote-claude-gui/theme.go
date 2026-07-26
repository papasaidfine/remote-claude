//go:build gui

package main

import (
	_ "embed"
	"image/color"

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

// The palette is warm neutral with a single clay accent, taken from the Claude
// marks this app already ships (see claude-front.svg). Fyne's stock blue-on-grey
// belongs to no product in particular; borrowing the accent the icons already use
// makes the window read as one piece with its own tray icon.
//
// Only the accent is saturated. Everything else is a warm grey, so state — a
// connected tunnel, a focused field, a destructive button — is the only colour
// on screen and reads instantly.
var (
	// dark variant
	darkBg        = rgb(0x1A1815)
	darkCard      = rgb(0x211E19)
	darkButton    = rgb(0x2A2621)
	darkInput     = rgb(0x232019)
	darkHover     = rgb(0x2E2924)
	darkSeparator = rgb(0x332E27)
	darkFg        = rgb(0xE8E3DC)
	darkDim       = rgb(0x8A8177)
	darkAccent    = rgb(0xD97757)

	// light variant
	lightBg        = rgb(0xFAF8F5)
	lightCard      = rgb(0xFFFFFF)
	lightButton    = rgb(0xF0EBE4)
	lightInput     = rgb(0xFFFFFF)
	lightHover     = rgb(0xEDE7DE)
	lightSeparator = rgb(0xE2DBD1)
	lightFg        = rgb(0x2B2622)
	lightDim       = rgb(0x8A8177)
	lightAccent    = rgb(0xC96442)
)

// Tunnel-state colours. Green and amber are muted rather than pure, so they sit
// beside the clay accent instead of fighting it.
var (
	darkUp       = rgb(0x6FBF8B)
	darkPending  = rgb(0xE0A458)
	lightUp      = rgb(0x4E9A6B)
	lightPending = rgb(0xC9803E)
)

func rgb(v uint32) color.NRGBA {
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xFF}
}

// rcTheme is the app's look: a CJK-capable font for every text style, and the
// warm palette above.
//
// One font for both Latin and CJK removes Fyne's per-glyph fallback — the thing
// that made Chinese jitter above and below the baseline. Bold keeps its own
// weight; italic and monospace use the regular CJK face (the usage table aligns
// with a real grid, not a monospace one).
type rcTheme struct{ fyne.Theme }

func newRCTheme() fyne.Theme { return &rcTheme{Theme: theme.DefaultTheme()} }

func (t *rcTheme) Font(s fyne.TextStyle) fyne.Resource {
	if s.Bold {
		return uiFontBold
	}
	return uiFontRegular
}

func (t *rcTheme) Color(name fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if v == theme.VariantLight {
		return t.lightColor(name)
	}
	return t.darkColor(name)
}

func (t *rcTheme) darkColor(name fyne.ThemeColorName) color.Color {
	switch name {
	case theme.ColorNameBackground:
		return darkBg
	case theme.ColorNameForeground:
		return darkFg
	case theme.ColorNamePrimary, theme.ColorNameFocus, theme.ColorNameHyperlink:
		return darkAccent
	case theme.ColorNameButton:
		return darkButton
	case theme.ColorNameInputBackground, theme.ColorNameOverlayBackground,
		theme.ColorNameMenuBackground:
		return darkInput
	case theme.ColorNameHover, theme.ColorNameSelection:
		return darkHover
	case theme.ColorNameSeparator, theme.ColorNameInputBorder:
		return darkSeparator
	case theme.ColorNamePlaceHolder, theme.ColorNameDisabled:
		return darkDim
	case theme.ColorNameSuccess:
		return darkUp
	case theme.ColorNameWarning:
		return darkPending
	}
	return t.Theme.Color(name, theme.VariantDark)
}

func (t *rcTheme) lightColor(name fyne.ThemeColorName) color.Color {
	switch name {
	case theme.ColorNameBackground:
		return lightBg
	case theme.ColorNameForeground:
		return lightFg
	case theme.ColorNamePrimary, theme.ColorNameFocus, theme.ColorNameHyperlink:
		return lightAccent
	case theme.ColorNameButton:
		return lightButton
	case theme.ColorNameInputBackground, theme.ColorNameOverlayBackground,
		theme.ColorNameMenuBackground:
		return lightInput
	case theme.ColorNameHover, theme.ColorNameSelection:
		return lightHover
	case theme.ColorNameSeparator, theme.ColorNameInputBorder:
		return lightSeparator
	case theme.ColorNamePlaceHolder, theme.ColorNameDisabled:
		return lightDim
	case theme.ColorNameSuccess:
		return lightUp
	case theme.ColorNameWarning:
		return lightPending
	}
	return t.Theme.Color(name, theme.VariantLight)
}

// Size widens the gaps between cards a little over Fyne's stock 4pt, at which
// they run together.
//
// SizeNameInnerPadding is deliberately left alone: it is a widget's *internal*
// padding, so raising it inflates the height of every button on screen rather
// than the space around the cards.
func (t *rcTheme) Size(name fyne.ThemeSizeName) float32 {
	if name == theme.SizeNamePadding {
		return 7
	}
	return t.Theme.Size(name)
}

// isLight reports whether the app is currently rendering the light variant, for
// the few places that pick a colour directly rather than through the theme.
func isLight(a fyne.App) bool {
	return a.Settings().ThemeVariant() == theme.VariantLight
}

// cardColor is the surface a host card sits on: one step off the window
// background, which separates cards without needing a rule between them.
func cardColor(a fyne.App) color.Color {
	if isLight(a) {
		return lightCard
	}
	return darkCard
}

func separatorColor(a fyne.App) color.Color {
	if isLight(a) {
		return lightSeparator
	}
	return darkSeparator
}

func dimColor(a fyne.App) color.Color {
	if isLight(a) {
		return lightDim
	}
	return darkDim
}

func upColor(a fyne.App) color.Color {
	if isLight(a) {
		return lightUp
	}
	return darkUp
}

func pendingColor(a fyne.App) color.Color {
	if isLight(a) {
		return lightPending
	}
	return darkPending
}
