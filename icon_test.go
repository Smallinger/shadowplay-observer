package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
)

func TestGlyphsParseAndRender(t *testing.T) {
	for name, g := range map[string]glyph{"active": glyphActive, "paused": glyphPaused} {
		if len(g) == 0 {
			t.Fatalf("%s: no shapes", name)
		}
		for _, n := range []int{16, 24, 32} {
			pix := renderIcon(n, g, colorActive, trayFill)
			lit := 0
			for i := 0; i < n*n; i++ {
				if pix[i*4+1] != colorBackground[1] { // green channel differs from the tile
					lit++
				}
				if pix[i*4+3] != 255 {
					t.Fatalf("%s/%d: pixel %d not opaque", name, n, i)
				}
			}
			// The logo covers roughly a quarter to two thirds of the icon.
			if lit < n*n/8 || lit > n*n*3/4 {
				t.Errorf("%s/%d: %d of %d pixels lit, looks wrong", name, n, lit, n*n)
			}
		}
	}
}

func TestPaintedCentre(t *testing.T) {
	// The centre dot of the active glyph sits at (12,12) of the 24 unit box.
	if !glyphActive.covers(12, 12) {
		t.Error("active glyph: centre dot missing")
	}
	// The ring's hollow between dot and ring must stay empty.
	if glyphActive.covers(12, 12-5.5) {
		t.Error("active glyph: gap between dot and ring is filled")
	}
}

// TestWriteLogo regenerates assets/logo.png (green logo on the dark grey tile, used in the README):
//
//	WRITE_LOGO=1 go test -run TestWriteLogo
func TestWriteLogo(t *testing.T) {
	if os.Getenv("WRITE_LOGO") == "" {
		t.Skip("set WRITE_LOGO=1 to regenerate assets/logo.png")
	}
	const n = 512
	pix := renderIcon(n, glyphActive, colorActive, logoFill)
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	for i := 0; i < n*n; i++ {
		img.Set(i%n, i/n, color.RGBA{pix[i*4+2], pix[i*4+1], pix[i*4], 255})
	}
	f, err := os.Create("assets/logo.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
