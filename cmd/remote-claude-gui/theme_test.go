//go:build gui

package main

import (
	"os"
	"testing"

	"golang.org/x/image/font/sfnt"
)

// The bundled UI fonts are Noto Sans SC subset to the characters that appear in
// internal/i18n/i18n.go. Adding a Chinese string without regenerating them is
// silent: the build passes, and Fyne falls back to a system font for the missing
// glyphs — which is what puts characters of one word on different baselines
// ("设" riding higher than "置"). Nothing catches that but a reader's eye, so
// this checks the same input the subsetting uses against what shipped.
//
// Regenerate with the commands in theme.go when this fails.
func TestBundledFontsCoverEveryUIString(t *testing.T) {
	src, err := os.ReadFile("../../internal/i18n/i18n.go")
	if err != nil {
		t.Fatalf("reading the message catalog: %v", err)
	}

	fonts := map[string][]byte{
		"uifont.otf":      uiFontRegularData,
		"uifont-bold.otf": uiFontBoldData,
	}
	for name, data := range fonts {
		f, err := sfnt.Parse(data)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		var buf sfnt.Buffer
		missing := map[rune]bool{}
		for _, r := range string(src) {
			if r < 0x20 || missing[r] {
				continue // control characters have no glyph and never render
			}
			idx, err := f.GlyphIndex(&buf, r)
			if err != nil || idx == 0 {
				missing[r] = true
			}
		}
		if len(missing) > 0 {
			var runes []rune
			for r := range missing {
				runes = append(runes, r)
			}
			t.Errorf("%s is missing %d character(s) used by the UI: %q\n"+
				"regenerate the font subsets — see the comment at the top of theme.go",
				name, len(runes), string(runes))
		}
	}
}
