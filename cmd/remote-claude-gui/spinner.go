//go:build gui

package main

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
)

// spinnerFrames is one turn of a spinner, as button icons.
//
// It has to be a set of static images because that is all a Fyne button's icon
// slot takes — widget.Activity is a widget and cannot go there, and putting it
// beside the button instead leaves the spinner outside the thing that is busy.
//
// Eight dots of fading opacity rather than a rotating arc: no transforms, no arc
// commands, nothing that Fyne's SVG renderer has to interpret. Each frame just
// moves which dot is the bright one.
var spinnerFrames = makeSpinnerFrames()

const spinnerDots = 8

func makeSpinnerFrames() []fyne.Resource {
	frames := make([]fyne.Resource, spinnerDots)
	for f := range frames {
		frames[f] = fyne.NewStaticResource(
			fmt.Sprintf("spinner-%d.svg", f), []byte(spinnerSVG(f)))
	}
	return frames
}

// spinnerSVG draws the ring with dot `lead` at full strength and the rest
// trailing off behind it.
func spinnerSVG(lead int) string {
	// Positions on a circle of radius 8 about (12,12), starting at the top.
	// Precomputed so the SVG carries no arithmetic for the renderer to do.
	type pt struct{ x, y string }
	ring := []pt{
		{"12", "4"}, {"17.66", "6.34"}, {"20", "12"}, {"17.66", "17.66"},
		{"12", "20"}, {"6.34", "17.66"}, {"4", "12"}, {"6.34", "6.34"},
	}
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24">`)
	for i, p := range ring {
		// How far behind the lead this dot is, 0 (brightest) to 7 (faintest).
		behind := (i - lead + spinnerDots) % spinnerDots
		opacity := 1.0 - float64(behind)*0.11
		fmt.Fprintf(&b, `<circle cx="%s" cy="%s" r="2.2" fill="#D77757" fill-opacity="%.2f"/>`,
			p.x, p.y, opacity)
	}
	b.WriteString(`</svg>`)
	return b.String()
}
