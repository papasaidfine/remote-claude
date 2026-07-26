package tui

import (
	"strings"
	"testing"
)

func TestBarFillsTheWidthAtFull(t *testing.T) {
	if got := bar(1, 10); got != strings.Repeat("█", 10) {
		t.Errorf("bar(1, 10) = %q, want 10 full blocks", got)
	}
}

func TestBarIsEmptyAtZero(t *testing.T) {
	if got := bar(0, 10); got != "" {
		t.Errorf("bar(0, 10) = %q, want empty", got)
	}
}

func TestBarIsHalfTheWidthAtHalf(t *testing.T) {
	if got := bar(0.5, 10); got != strings.Repeat("█", 5) {
		t.Errorf("bar(0.5, 10) = %q, want 5 full blocks", got)
	}
}

// A model that cost a fraction of the leader still has to be visible; rounding
// its bar away would read as "no usage" rather than "a little".
func TestBarShowsAPartialBlockForASmallShare(t *testing.T) {
	got := bar(0.02, 10)
	if got == "" {
		t.Fatal("bar(0.02, 10) rendered nothing; a small share must still be visible")
	}
	if []rune(got)[0] == '█' {
		t.Errorf("bar(0.02, 10) = %q, want a partial block, not a full one", got)
	}
}

// Eighth-blocks give sub-cell resolution, so neighbouring values stay distinct
// in a narrow terminal instead of collapsing onto the same bar.
func TestBarUsesEighthBlocksForSubCellPrecision(t *testing.T) {
	quarter := bar(0.025, 10) // a quarter of one cell
	half := bar(0.05, 10)     // half of one cell
	if quarter == half {
		t.Errorf("bar cannot tell 1/4 of a cell from 1/2: both %q", quarter)
	}
}

func TestBarClampsOutOfRangeFractions(t *testing.T) {
	if got := bar(1.5, 8); got != strings.Repeat("█", 8) {
		t.Errorf("bar(1.5, 8) = %q, want it clamped to 8 full blocks", got)
	}
	if got := bar(-1, 8); got != "" {
		t.Errorf("bar(-1, 8) = %q, want empty", got)
	}
}

func TestBarNeverExceedsTheGivenWidth(t *testing.T) {
	for _, f := range []float64{0, 0.01, 0.33, 0.5, 0.99, 1} {
		if n := len([]rune(bar(f, 12))); n > 12 {
			t.Errorf("bar(%v, 12) is %d cells wide, want at most 12", f, n)
		}
	}
}
