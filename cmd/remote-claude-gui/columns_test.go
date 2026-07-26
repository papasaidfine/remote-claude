//go:build gui

package main

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// cell is a stand-in for a label of a known size.
func cell(w, h float32) fyne.CanvasObject {
	r := canvas.NewRectangle(nil)
	r.SetMinSize(fyne.NewSize(w, h))
	return r
}

func widthsOf(objs []fyne.CanvasObject) []float32 {
	out := make([]float32, len(objs))
	for i, o := range objs {
		out[i] = o.Size().Width
	}
	return out
}

// Each column is only as wide as its own widest cell — a column of "1.2M" must
// not be handed the same width as one holding model names.
func TestColumnsSizeToTheirOwnContent(t *testing.T) {
	objs := []fyne.CanvasObject{
		cell(200, 20), cell(40, 20), // row 1
		cell(120, 20), cell(60, 20), // row 2
	}
	l := columnsLayout{cols: 2, grow: -1}
	l.Layout(objs, fyne.NewSize(1000, 100))

	got := widthsOf(objs)
	if got[0] != 200 || got[2] != 200 {
		t.Errorf("column 0 widths = %v, %v; want both 200 (its widest cell)", got[0], got[2])
	}
	if got[1] != 60 || got[3] != 60 {
		t.Errorf("column 1 widths = %v, %v; want both 60 (its widest cell)", got[1], got[3])
	}
}

func TestMinSizeIsTheSumOfColumnWidths(t *testing.T) {
	objs := []fyne.CanvasObject{cell(200, 20), cell(40, 20), cell(120, 25), cell(60, 20)}
	l := columnsLayout{cols: 2, grow: -1}

	min := l.MinSize(objs)
	if min.Width != 260 {
		t.Errorf("MinSize().Width = %v, want 260 (200 + 60)", min.Width)
	}
	if min.Height != 45 {
		t.Errorf("MinSize().Height = %v, want 45 (20 + 25)", min.Height)
	}
}

// Leftover width goes to one nominated column rather than being shared out, so
// the number columns stay tight against the right edge instead of drifting apart.
func TestSlackGoesToTheGrowingColumn(t *testing.T) {
	objs := []fyne.CanvasObject{cell(100, 20), cell(50, 20)}
	l := columnsLayout{cols: 2, grow: 0}
	l.Layout(objs, fyne.NewSize(400, 20))

	got := widthsOf(objs)
	if got[0] != 350 {
		t.Errorf("growing column = %v, want 350 (100 + 250 of slack)", got[0])
	}
	if got[1] != 50 {
		t.Errorf("fixed column = %v, want its content width of 50", got[1])
	}
}

func TestRowsAreLaidOutLeftToRightTopToBottom(t *testing.T) {
	objs := []fyne.CanvasObject{cell(100, 20), cell(50, 30), cell(100, 20), cell(50, 20)}
	l := columnsLayout{cols: 2, grow: -1}
	l.Layout(objs, fyne.NewSize(400, 100))

	if objs[0].Position().X != 0 || objs[0].Position().Y != 0 {
		t.Errorf("first cell at %v, want 0,0", objs[0].Position())
	}
	if objs[1].Position().X != 100 {
		t.Errorf("second cell X = %v, want 100 (after column 0)", objs[1].Position().X)
	}
	// row 2 starts below the tallest cell of row 1
	if objs[2].Position().Y != 30 {
		t.Errorf("second row Y = %v, want 30 (below the 30-tall cell above)", objs[2].Position().Y)
	}
}

// Squeezed below its natural width, the layout takes the space back from the
// growing column first — the number columns are the ones that must stay legible.
func TestSqueezingShrinksTheGrowingColumnFirst(t *testing.T) {
	objs := []fyne.CanvasObject{cell(200, 20), cell(50, 20)}
	l := columnsLayout{cols: 2, grow: 0}
	l.Layout(objs, fyne.NewSize(150, 20))

	got := widthsOf(objs)
	if got[1] != 50 {
		t.Errorf("fixed column = %v, want it held at 50", got[1])
	}
	if got[0] != 100 {
		t.Errorf("growing column = %v, want 100 (what is left of 150)", got[0])
	}
}
