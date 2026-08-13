package pdfgen

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func opaqueImage(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 30, B: 40, A: 255})
		}
	}
	return img
}

func transparentImage(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a := uint8(255)
			if x < w/2 {
				a = 0 // left half fully transparent
			}
			img.Set(x, y, color.NRGBA{R: 10, G: 200, B: 90, A: a})
		}
	}
	return img
}

func build(t *testing.T, j Job) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Build(j, &buf); err != nil {
		t.Fatalf("Build: %v", err)
	}
	return buf.Bytes()
}

// A hand-written cross-reference table is the easiest thing to get subtly
// wrong, and a reader will reject the file outright if it is. Verify every
// offset actually lands on the object it claims.
func TestCrossReferenceTableIsValid(t *testing.T) {
	for _, mode := range []WhiteMode{WhiteNone, WhiteAuto, WhiteFull} {
		t.Run(mode.String(), func(t *testing.T) {
			pdf := build(t, Job{Image: transparentImage(8, 12), WidthMM: 56, HeightMM: 108.5, White: mode})
			assertXrefValid(t, pdf)
		})
	}
}

func assertXrefValid(t *testing.T, pdf []byte) {
	t.Helper()
	if !bytes.HasPrefix(pdf, []byte("%PDF-1.7")) {
		t.Fatal("missing PDF header")
	}
	if !bytes.HasSuffix(pdf, []byte("%%EOF\n")) {
		t.Fatal("missing EOF marker")
	}

	m := regexp.MustCompile(`startxref\n(\d+)\n%%EOF`).FindSubmatch(pdf)
	if m == nil {
		t.Fatal("no startxref")
	}
	start, err := strconv.Atoi(string(m[1]))
	if err != nil || start <= 0 || start >= len(pdf) {
		t.Fatalf("bad startxref %q", m[1])
	}
	if !bytes.HasPrefix(pdf[start:], []byte("xref\n0 ")) {
		t.Fatalf("startxref does not point at the xref table, found %q", snippet(pdf[start:]))
	}

	head := regexp.MustCompile(`^xref\n0 (\d+)\n`).FindSubmatch(pdf[start:])
	if head == nil {
		t.Fatal("malformed xref header")
	}
	count, _ := strconv.Atoi(string(head[1]))
	entries := pdf[start+len(head[0]):]

	for i := 1; i < count; i++ {
		entry := entries[i*20 : i*20+20] // entry 0 is the free-list head
		off, err := strconv.Atoi(strings.TrimSpace(string(entry[:10])))
		if err != nil {
			t.Fatalf("object %d: unparseable offset %q", i, entry)
		}
		want := fmt.Sprintf("%d 0 obj", i)
		if off < 0 || off >= len(pdf) || !bytes.HasPrefix(pdf[off:], []byte(want)) {
			t.Errorf("object %d: offset %d points at %q, want %q", i, off, snippet(pdf[off:]), want)
		}
	}
}

func snippet(b []byte) string {
	if len(b) > 24 {
		b = b[:24]
	}
	return string(b)
}

// The page must be exactly the artboard size. 56 mm = 158.74 pt.
func TestPageIsExactArtboardSize(t *testing.T) {
	pdf := build(t, Job{Image: opaqueImage(4, 8), WidthMM: 56, HeightMM: 108.5, White: WhiteNone})
	m := regexp.MustCompile(`/MediaBox\[0 0 ([\d.]+) ([\d.]+)\]`).FindSubmatch(pdf)
	if m == nil {
		t.Fatal("no MediaBox")
	}
	gotW, _ := strconv.ParseFloat(string(m[1]), 64)
	gotH, _ := strconv.ParseFloat(string(m[2]), 64)
	wantW, wantH := 56*72/25.4, 108.5*72/25.4
	if diff := gotW - wantW; diff > 1e-6 || diff < -1e-6 {
		t.Errorf("page width = %v pt, want %v", gotW, wantW)
	}
	if diff := gotH - wantH; diff > 1e-6 || diff < -1e-6 {
		t.Errorf("page height = %v pt, want %v", gotH, wantH)
	}
}

// The guide allows CMYK and named spot colours only.
func TestNoRGBReachesTheFile(t *testing.T) {
	pdf := build(t, Job{Image: opaqueImage(6, 6), WidthMM: 62, HeightMM: 117, White: WhiteFull})
	if bytes.Contains(pdf, []byte("/DeviceRGB")) {
		t.Error("file contains a DeviceRGB colour space")
	}
	if !bytes.Contains(pdf, []byte("/DeviceCMYK")) {
		t.Error("file should place artwork in DeviceCMYK")
	}
}

func TestSpotColourAndLayerNamesAreExact(t *testing.T) {
	pdf := build(t, Job{Image: opaqueImage(6, 6), WidthMM: 62, HeightMM: 117, White: WhiteFull})
	for _, want := range []string{
		"[/Separation/RDG_WHITE/DeviceCMYK", // spot colour, exact case
		"/Type/OCG/Name(RDG_WHITE)",         // layer named for the ink
		"/Type/OCG/Name(CMYK)",              // artwork layer
		"/OCProperties",
	} {
		if !bytes.Contains(pdf, []byte(want)) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestWhiteNoneOmitsTheWhiteLayer(t *testing.T) {
	pdf := build(t, Job{Image: opaqueImage(6, 6), WidthMM: 35, HeightMM: 89, White: WhiteNone})
	if bytes.Contains(pdf, []byte("RDG_WHITE")) {
		t.Error("white mode none should not emit an RDG_WHITE layer")
	}
	if !bytes.Contains(pdf, []byte("/Type/OCG/Name(CMYK)")) {
		t.Error("CMYK layer should still be present")
	}
}

// Auto mode follows the artwork: a transparent image gets a white image whose
// samples are the alpha channel, not a flood fill.
func TestAutoWhiteFollowsTransparency(t *testing.T) {
	pdf := build(t, Job{Image: transparentImage(8, 8), WidthMM: 35, HeightMM: 89, White: WhiteAuto})
	if !bytes.Contains(pdf, []byte("RDG_WHITE")) {
		t.Fatal("transparent artwork should still get a white undercoat")
	}
	if !bytes.Contains(pdf, []byte("/SMask")) {
		t.Error("transparent artwork should carry a soft mask so the bare enclosure shows through")
	}
}

// A fully opaque image in auto mode covers the whole artboard, so the cheaper
// rectangle is used instead of an image.
func TestAutoWhiteOnOpaqueArtworkUsesARectangle(t *testing.T) {
	pdf := build(t, Job{Image: opaqueImage(8, 8), WidthMM: 35, HeightMM: 89, White: WhiteAuto})
	if !bytes.Contains(pdf, []byte("RDG_WHITE")) {
		t.Fatal("opaque artwork should get a white undercoat")
	}
	if bytes.Contains(pdf, []byte("/SMask")) {
		t.Error("opaque artwork needs no soft mask")
	}
}

func TestRejectsBadInput(t *testing.T) {
	cases := map[string]Job{
		"no image":     {WidthMM: 56, HeightMM: 108.5},
		"zero artwork": {Image: image.NewRGBA(image.Rect(0, 0, 0, 0)), WidthMM: 56, HeightMM: 108.5},
		"zero board":   {Image: opaqueImage(4, 4)},
	}
	for name, j := range cases {
		if err := Build(j, &bytes.Buffer{}); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestParseWhiteMode(t *testing.T) {
	for in, want := range map[string]WhiteMode{
		"none": WhiteNone, "auto": WhiteAuto, "": WhiteAuto, "FULL": WhiteFull,
	} {
		got, err := ParseWhiteMode(in)
		if err != nil {
			t.Errorf("ParseWhiteMode(%q): %v", in, err)
		} else if got != want {
			t.Errorf("ParseWhiteMode(%q) = %v, want %v", in, got, want)
		}
	}
	if _, err := ParseWhiteMode("beige"); err == nil {
		t.Error("expected an error for an unknown mode")
	}
}

// Semi-transparent artwork must not darken: the encoder has to undo alpha
// premultiplication before converting to CMYK.
func TestSemiTransparentColourIsNotMuddied(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.NRGBA{R: 255, G: 0, B: 0, A: 128}) // half-opaque pure red

	cmyk, alpha, transparent := encodeCMYK(img)
	if !transparent {
		t.Fatal("image should be reported as transparent")
	}
	if alpha[0] != 128 {
		t.Errorf("alpha = %d, want 128", alpha[0])
	}
	// Pure red is C=0, M=255, Y=255, K=0 regardless of opacity.
	if cmyk[0] != 0 || cmyk[1] != 255 || cmyk[2] != 255 || cmyk[3] != 0 {
		t.Errorf("CMYK = %v, want [0 255 255 0]: alpha premultiplication was not undone", cmyk[:4])
	}
}
