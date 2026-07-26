package tui

import "strings"

// eighths are the partial block characters, 1/8 cell to 7/8 cell. They give a
// bar sub-cell resolution, which matters in a narrow terminal where whole cells
// alone would collapse neighbouring values onto the same length.
var eighths = []rune{'▏', '▎', '▍', '▌', '▋', '▊', '▉'}

// bar renders a horizontal bar of frac (0..1) across width cells.
//
// A non-zero share always renders something: rounding a small model's bar away
// would read as "no usage" when the truth is "a little", and that is the one
// reading a usage chart must never invite.
func bar(frac float64, width int) string {
	if width <= 0 || frac <= 0 {
		return ""
	}
	if frac > 1 {
		frac = 1
	}
	total := frac * float64(width) * 8 // in eighths of a cell
	full := int(total) / 8
	rest := int(total) % 8
	if full >= width {
		return strings.Repeat("█", width)
	}
	var b strings.Builder
	b.WriteString(strings.Repeat("█", full))
	switch {
	case rest > 0:
		b.WriteRune(eighths[rest-1])
	case full == 0:
		b.WriteRune(eighths[0]) // too small to round to an eighth, but not zero
	}
	return b.String()
}
