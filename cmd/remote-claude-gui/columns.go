//go:build gui

package main

import "fyne.io/fyne/v2"

// columnsLayout arranges a flat list of cells as a grid, sizing each column to
// its own widest cell rather than splitting the width evenly.
//
// Fyne's GridLayout gives every column an equal share, which on a usage table
// means a column of "1.2M" is handed as much room as one holding model names.
// That both wastes the space and inflates the table's minimum width until it no
// longer fits and has to be scrolled sideways. Measuring each column instead
// makes the table fit in far less room and look deliberate at any size.
//
// grow names the column that absorbs leftover width — or gives width back first
// when the space is tight. Use it for the text column, so the number columns
// stay tight together and legible. -1 means no column grows.
type columnsLayout struct {
	cols int
	grow int
}

// columnWidths is each column's natural width: its widest cell.
func (l columnsLayout) columnWidths(objs []fyne.CanvasObject) []float32 {
	w := make([]float32, l.cols)
	for i, o := range objs {
		c := i % l.cols
		if m := o.MinSize().Width; m > w[c] {
			w[c] = m
		}
	}
	return w
}

// rowHeights is each row's natural height: its tallest cell.
func (l columnsLayout) rowHeights(objs []fyne.CanvasObject) []float32 {
	h := make([]float32, 0, (len(objs)+l.cols-1)/l.cols)
	for i, o := range objs {
		if i%l.cols == 0 {
			h = append(h, 0)
		}
		r := len(h) - 1
		if m := o.MinSize().Height; m > h[r] {
			h[r] = m
		}
	}
	return h
}

func (l columnsLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var size fyne.Size
	for _, w := range l.columnWidths(objs) {
		size.Width += w
	}
	for _, h := range l.rowHeights(objs) {
		size.Height += h
	}
	return size
}

func (l columnsLayout) Layout(objs []fyne.CanvasObject, avail fyne.Size) {
	widths := l.columnWidths(objs)
	var natural float32
	for _, w := range widths {
		natural += w
	}
	// Hand the difference — either way — to the growing column alone.
	if l.grow >= 0 && l.grow < l.cols {
		widths[l.grow] += avail.Width - natural
		if widths[l.grow] < 0 {
			widths[l.grow] = 0
		}
	}
	heights := l.rowHeights(objs)

	var y float32
	for row := 0; row*l.cols < len(objs); row++ {
		var x float32
		for col := 0; col < l.cols; col++ {
			i := row*l.cols + col
			if i >= len(objs) {
				break
			}
			objs[i].Resize(fyne.NewSize(widths[col], heights[row]))
			objs[i].Move(fyne.NewPos(x, y))
			x += widths[col]
		}
		y += heights[row]
	}
}
