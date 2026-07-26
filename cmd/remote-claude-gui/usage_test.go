//go:build gui

package main

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

func layoutBar(frac float32, width float32) fyne.Size {
	r := canvas.NewRectangle(nil)
	objs := []fyne.CanvasObject{r}
	barLayout{frac: frac}.Layout(objs, fyne.NewSize(width, 14))
	return r.Size()
}

func TestBarSpansItsShareOfTheWidth(t *testing.T) {
	if got := layoutBar(0.5, 200).Width; got != 100 {
		t.Errorf("half of 200 = %v, want 100", got)
	}
	if got := layoutBar(1, 200).Width; got != 200 {
		t.Errorf("all of 200 = %v, want 200", got)
	}
}

// A model that cost a sliver of the leader still has to register. Rounding its
// bar to nothing would read as "this model was not used", which is a different
// and wrong statement.
func TestBarStaysVisibleForATinyShare(t *testing.T) {
	got := layoutBar(0.001, 200).Width
	if got <= 0 {
		t.Fatalf("a 0.1%% share rendered %v wide — it vanished", got)
	}
	if got > 6 {
		t.Errorf("a 0.1%% share rendered %v wide — too wide to be honest", got)
	}
}

func TestBarIsAbsentAtZero(t *testing.T) {
	if got := layoutBar(0, 200).Width; got != 0 {
		t.Errorf("zero cost drew a bar %v wide, want none", got)
	}
}

func TestBarNeverOverflowsItsTrack(t *testing.T) {
	for _, f := range []float32{0, 0.25, 0.99, 1, 1.5} {
		if got := layoutBar(f, 120).Width; got > 120 {
			t.Errorf("frac %v drew %v wide, want at most 120", f, got)
		}
	}
}

func TestCostSharePicksThePeakAsFullScale(t *testing.T) {
	costs := []float64{8.20, 3.10, 0.18}
	got := costShares(costs)
	if got[0] != 1 {
		t.Errorf("the dearest model is %v of full scale, want 1", got[0])
	}
	if want := float32(3.10 / 8.20); got[1] < want-0.001 || got[1] > want+0.001 {
		t.Errorf("second model = %v, want %v", got[1], want)
	}
}

func TestCostSharesSurviveAnAllZeroWindow(t *testing.T) {
	for i, f := range costShares([]float64{0, 0, 0}) {
		if f != 0 {
			t.Errorf("share %d = %v with no cost anywhere, want 0", i, f)
		}
	}
}
