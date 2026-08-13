package pdfinspect_test

// These tests close the loop: pdfgen writes a file, pdfinspect reads it back,
// and the two have to agree. Both sides of this repo's PDF handling are
// hand-rolled, so a shared misunderstanding would otherwise go unnoticed —
// the writer's own tests cannot catch a mistake the writer is consistent
// about. The reader was written from the spec, not from the writer.

import (
	"bytes"
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/laneholloway/tayda-uv-artwork-processor/internal/enclosure"
	"github.com/laneholloway/tayda-uv-artwork-processor/internal/pdfgen"
	"github.com/laneholloway/tayda-uv-artwork-processor/internal/pdfinspect"
)

func opaque(w, h int) image.Image {
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m.Set(x, y, color.RGBA{200, 40, 60, 255})
		}
	}
	return m
}

func build(t *testing.T, j pdfgen.Job) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := pdfgen.Build(j, &buf); err != nil {
		t.Fatalf("Build: %v", err)
	}
	return buf.Bytes()
}

func inspect(t *testing.T, b []byte) pdfinspect.Result {
	t.Helper()
	r, err := pdfinspect.Inspect(b)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	return r
}

// Every side of every enclosure must come back out at the size it went in,
// which is the single fact a wrong value here would ruin an enclosure over.
func TestEveryArtboardSizeSurvivesTheRoundTrip(t *testing.T) {
	for _, name := range enclosure.Names() {
		e, err := enclosure.Lookup(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, side := range enclosure.Sides {
			size, err := e.Size(side)
			if err != nil {
				t.Fatal(err)
			}
			b := build(t, pdfgen.Job{
				Image: opaque(40, 40), WidthMM: size.WidthMM, HeightMM: size.HeightMM,
				White: pdfgen.WhiteNone,
			})
			r := inspect(t, b)

			if d := r.WidthMM - size.WidthMM; d > 0.01 || d < -0.01 {
				t.Errorf("%s %s: width read back as %v mm, want %v", name, side, r.WidthMM, size.WidthMM)
			}
			if d := r.HeightMM - size.HeightMM; d > 0.01 || d < -0.01 {
				t.Errorf("%s %s: height read back as %v mm, want %v", name, side, r.HeightMM, size.HeightMM)
			}
			// And the size must identify the side it was built for.
			var found bool
			for _, m := range enclosure.MatchSize(r.WidthMM, r.HeightMM) {
				if m.Enclosure == name && m.Side == side {
					found = true
				}
			}
			if !found {
				t.Errorf("%s %s: %v × %v mm did not match its own side back",
					name, side, r.WidthMM, r.HeightMM)
			}
		}
	}
}

// The guide's checklist calls White → CMYK → Gloss VERY IMPORTANT. Read it
// back from the finished file for every combination that produces layers.
func TestPrintOrderReadsBackCorrectly(t *testing.T) {
	cases := []struct {
		white pdfgen.WhiteMode
		gloss pdfgen.GlossMode
		want  []string
	}{
		{pdfgen.WhiteNone, pdfgen.GlossNone, []string{"CMYK"}},
		{pdfgen.WhiteFull, pdfgen.GlossNone, []string{"RDG_WHITE", "CMYK"}},
		{pdfgen.WhiteAuto, pdfgen.GlossNone, []string{"RDG_WHITE", "CMYK"}},
		{pdfgen.WhiteNone, pdfgen.GlossFull, []string{"CMYK", "RDG_GLOSS"}},
		{pdfgen.WhiteFull, pdfgen.GlossFull, []string{"RDG_WHITE", "CMYK", "RDG_GLOSS"}},
		{pdfgen.WhiteFull, pdfgen.GlossArtwork, []string{"RDG_WHITE", "CMYK", "RDG_GLOSS"}},
	}
	for _, c := range cases {
		b := build(t, pdfgen.Job{
			Image: opaque(40, 40), WidthMM: 56, HeightMM: 108.5,
			White: c.white, Gloss: c.gloss,
		})
		r := inspect(t, b)
		got := strings.Join(r.PaintOrder, " → ")
		want := strings.Join(c.want, " → ")
		if got != want {
			t.Errorf("white=%v gloss=%v: paint order %q, want %q", c.white, c.gloss, got, want)
		}
	}
}

// No RGB may reach the file. pdfgen has its own test asserting the literal
// /DeviceRGB is absent; this one asserts it through a reader that decompresses
// the streams, where a leak would actually hide.
func TestNoRGBSurvivesIntoTheFile(t *testing.T) {
	b := build(t, pdfgen.Job{
		Image: opaque(40, 40), WidthMM: 56, HeightMM: 108.5,
		White: pdfgen.WhiteFull, Gloss: pdfgen.GlossFull,
	})
	r := inspect(t, b)
	if r.HasRGB() {
		t.Errorf("colour spaces = %v, want no RGB", r.ColorSpaces)
	}
	var sawCMYK bool
	for _, cs := range r.ColorSpaces {
		if cs == "DeviceCMYK" {
			sawCMYK = true
		}
	}
	if !sawCMYK {
		t.Errorf("colour spaces = %v, want DeviceCMYK among them", r.ColorSpaces)
	}
}

// The spot names are case-sensitive and the guide says so explicitly.
func TestSpotNamesReadBackExactly(t *testing.T) {
	b := build(t, pdfgen.Job{
		Image: opaque(40, 40), WidthMM: 56, HeightMM: 108.5,
		White: pdfgen.WhiteFull, Gloss: pdfgen.GlossFull,
	})
	r := inspect(t, b)
	want := []string{"RDG_GLOSS", "RDG_WHITE"} // sorted
	if strings.Join(r.SpotColors, ",") != strings.Join(want, ",") {
		t.Errorf("spots = %v, want %v", r.SpotColors, want)
	}
}

// A coverage image takes a different path through the writer than a flood
// fill, so it needs its own round trip.
func TestCoverageImagesReadBackTheSame(t *testing.T) {
	mask := image.NewRGBA(image.Rect(0, 0, 20, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			v := uint8(0)
			if x < 10 {
				v = 255
			}
			mask.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	b := build(t, pdfgen.Job{
		Image: opaque(40, 40), WidthMM: 56, HeightMM: 108.5,
		White: pdfgen.WhiteFull, Gloss: pdfgen.GlossMask, GlossMask: mask,
	})
	r := inspect(t, b)
	if got := strings.Join(r.PaintOrder, " → "); got != "RDG_WHITE → CMYK → RDG_GLOSS" {
		t.Errorf("paint order = %q", got)
	}
	if r.HasRGB() {
		t.Errorf("colour spaces = %v, want no RGB", r.ColorSpaces)
	}
}

// One artboard per file is what the Tayda Box Tool expects.
func TestOutputIsAlwaysASinglePage(t *testing.T) {
	b := build(t, pdfgen.Job{
		Image: opaque(40, 40), WidthMM: 56, HeightMM: 108.5,
		White: pdfgen.WhiteFull, Gloss: pdfgen.GlossFull,
	})
	if r := inspect(t, b); r.Pages != 1 {
		t.Errorf("pages = %d, want 1", r.Pages)
	}
}
