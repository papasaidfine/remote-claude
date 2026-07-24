//go:build gui

package main

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

//go:embed claude.svg
var claudeSVG []byte

// appIcon is the Claude logo used for the window/taskbar icon and the system
// tray icon. Fyne rasterizes the SVG resource at whatever size each surface
// needs.
var appIcon = fyne.NewStaticResource("claude.svg", claudeSVG)
