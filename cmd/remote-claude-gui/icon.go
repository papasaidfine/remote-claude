//go:build gui

package main

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

//go:embed claude.svg
var claudeSVG []byte

// appIcon is the Claude logo used for the window/taskbar icon and the system
// tray icon while the app is running. Fyne rasterizes the SVG at whatever size
// each surface needs.
//
// The icon on the downloaded artifact itself is separate — SetIcon can't do it:
//   - Windows: rsrc_windows_amd64.syso (a compiled .rsrc the Go linker embeds
//     into the .exe automatically). Icon.png here is the source.
//   - macOS: the release workflow builds a .app whose Resources/icon.icns is
//     generated from Icon.png (sips + iconutil), then wraps it in a .dmg.
//   - Linux: executables carry no icon (that's a .desktop-file concern).
//
// Regenerate the Windows resource after changing the logo:
//
//	uv run --with cairosvg --with pillow python -c 'import cairosvg;\
//	  cairosvg.svg2png(url="claude.svg",write_to="Icon.png",output_width=1024,output_height=1024)'
//	# then Icon.png -> multi-size Icon.ico (Pillow) -> rsrc:
//	go run github.com/akavel/rsrc@latest -ico Icon.ico -arch amd64 -o rsrc_windows_amd64.syso
var appIcon = fyne.NewStaticResource("claude.svg", claudeSVG)
