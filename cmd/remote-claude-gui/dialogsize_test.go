//go:build gui

package main

import (
	"testing"

	"fyne.io/fyne/v2"
)

func TestDialogTakesMostOfARoomyWindow(t *testing.T) {
	got := dialogSize(fyne.NewSize(1600, 1000), fyne.NewSize(680, 520))
	if got.Width <= 680 {
		t.Errorf("width %v in a 1600px window, want more than the 680 minimum", got.Width)
	}
	if got.Width >= 1600 || got.Height >= 1000 {
		t.Errorf("size %v fills the whole window; it should leave a margin", got)
	}
}

// The usage table needs a certain width before its last column falls off the
// edge, so a small window must not shrink the dialog below it — the content
// scrolls instead.
func TestDialogNeverGoesBelowItsMinimum(t *testing.T) {
	got := dialogSize(fyne.NewSize(400, 300), fyne.NewSize(680, 520))
	if got.Width != 680 || got.Height != 520 {
		t.Errorf("size %v in a small window, want the 680x520 minimum held", got)
	}
}

func TestDialogGrowsWithTheWindow(t *testing.T) {
	small := dialogSize(fyne.NewSize(900, 700), fyne.NewSize(680, 520))
	large := dialogSize(fyne.NewSize(1400, 700), fyne.NewSize(680, 520))
	if large.Width <= small.Width {
		t.Errorf("a wider window gave no more width: %v then %v", small.Width, large.Width)
	}
}
