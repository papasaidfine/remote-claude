//go:build gui

package main

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

// The app ships three Claude marks. Fyne rasterizes each SVG at whatever size the
// surface it lands on needs:
//
//   - appIcon  (claude-front.svg): the window / taskbar icon — Claude face-on.
//   - trayIcon (claude-tray.svg):  the system-tray icon while the app runs; the
//     classic logo reads better than the face at 16px.
//   - surfIcon (claude-surf.svg):  the "hang tight" mark the waiting() panel shows
//     in dialogs that block on the network (usage, server setup, updates).

//go:embed claude-front.svg
var claudeFrontSVG []byte

//go:embed claude-tray.svg
var claudeTraySVG []byte

//go:embed claude-surf.svg
var claudeSurfSVG []byte

// Marks for the two per-host launch buttons:
//
//   - vscode.svg  the Visual Studio Code logo (a Microsoft trademark, used here
//     only to point at the editor the button opens).
//   - clawd.svg   Clawd, in the same clay as this window's accent.
//
// Both carry their own fills, so both are drawn raw. Wrapping either in a
// ThemedResource repaints every shape a single colour and flattens it into an
// unreadable blob — which is not obvious until you render it.

//go:embed vscode.svg
var vscodeSVG []byte

//go:embed clawd.svg
var clawdSVG []byte

// The icon on the downloaded artifact itself is separate — SetIcon can't do it:
//   - Windows: rsrc_windows_amd64.syso (a compiled .rsrc the Go linker embeds
//     into the .exe automatically). Icon.png here is the source.
//   - macOS: the release workflow builds a .app whose Resources/icon.icns is
//     generated from Icon.png (sips + iconutil), then wraps it in a .dmg.
//   - Linux: executables carry no icon (that's a .desktop-file concern).
//
// Regenerate the Windows resource after changing the logo (source: claude-front.svg):
//
//	uv run --with cairosvg --with pillow python -c 'import cairosvg;\
//	  cairosvg.svg2png(url="claude-front.svg",write_to="Icon.png",output_width=1024,output_height=1024)'
//	# then Icon.png -> multi-size Icon.ico (Pillow) -> rsrc:
//	go run github.com/akavel/rsrc@latest -ico Icon.ico -arch amd64 -o rsrc_windows_amd64.syso
var (
	appIcon    = fyne.NewStaticResource("claude-front.svg", claudeFrontSVG)
	trayIcon   = fyne.NewStaticResource("claude-tray.svg", claudeTraySVG)
	surfIcon   = fyne.NewStaticResource("claude-surf.svg", claudeSurfSVG)
	vscodeIcon = fyne.NewStaticResource("vscode.svg", vscodeSVG)
	clawdIcon  = fyne.NewStaticResource("clawd.svg", clawdSVG)
)
