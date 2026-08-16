package vector

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drlholloway/tayda-uv-artwork-processor/internal/artwork"
	"github.com/drlholloway/tayda-uv-artwork-processor/internal/enclosure"
)

// write drops an SVG in a temp dir and returns its path.
func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "art.svg")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// svgFor wraps body in an <svg> with the given viewBox.
func svgFor(viewW, viewH float64, body string) string {
	return fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %g %g">%s</svg>`,
		viewW, viewH, body)
}

const filled = `<rect x="0" y="0" width="100%" height="100%" fill="#c02020"/>`

func TestIsVector(t *testing.T) {
	for _, c := range []struct {
		path string
		want bool
	}{
		{"face.svg", true},
		{"FACE.SVG", true},
		{"face.Svg", true},
		{"face.png", false},
		{"face.svg.png", false},
		{"svg", false},
	} {
		if got := IsVector(c.path); got != c.want {
			t.Errorf("IsVector(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// A 1590B side A is 56 × 108.50 mm. Artwork drawn to that shape should come
// back on a canvas of exactly those proportions, so the only thing
// artwork.Check can complain about is the artwork.
func TestArtworkDrawnForTheSideGetsAClearReport(t *testing.T) {
	size := sideSize(t, "1590B", enclosure.SideA)

	// A viewBox in the side's proportions, written the way a drawing program
	// would: in millimetres.
	p := write(t, svgFor(size.WidthMM, size.HeightMM, filled))
	img, err := Load(p, size.WidthMM, size.HeightMM, DefaultDPI)
	if err != nil {
		t.Fatal(err)
	}

	rep := artwork.Check(img, enclosure.SideA, size)
	if !rep.OK() {
		t.Fatalf("problems on artwork drawn to size: %v", rep.Problems)
	}
	if len(rep.Warnings) != 0 {
		t.Errorf("warnings on artwork drawn to size: %v", rep.Warnings)
	}

	b := img.Bounds()
	if got, want := float64(b.Dx())/float64(b.Dy()), size.AspectRatio(); got != want {
		t.Errorf("canvas aspect = %v, artboard aspect = %v; want them identical", got, want)
	}
}

// The whole point of sizing from the SVG rather than the artboard: artwork of
// the wrong shape must stay the wrong shape, or validation is measuring a
// canvas this package just made up.
func TestWrongShapeArtworkIsStillReported(t *testing.T) {
	size := sideSize(t, "1590B", enclosure.SideA) // 56 × 108.50 mm, portrait

	p := write(t, svgFor(100, 100, filled)) // square
	img, err := Load(p, size.WidthMM, size.HeightMM, DefaultDPI)
	if err != nil {
		t.Fatal(err)
	}

	b := img.Bounds()
	if b.Dx() != b.Dy() {
		t.Errorf("square SVG rendered %d × %d px; want a square canvas", b.Dx(), b.Dy())
	}

	rep := artwork.Check(img, enclosure.SideA, size)
	if rep.OK() {
		t.Fatal("square artwork on a portrait side reported no problems")
	}
	if !containsAny(rep.Problems, "aspect ratio") {
		t.Errorf("problems do not mention the aspect ratio: %v", rep.Problems)
	}
}

// Rasterizing must clear the guide's resolution floor on both axes, whatever
// shape the artwork is, or a conversion that should succeed fails on DPI.
func TestEveryArtboardClearsTheResolutionFloor(t *testing.T) {
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
			// A matching shape, and a mismatched one that is still a
			// plausible mistake rather than an absurd one.
			for _, view := range [][2]float64{
				{size.WidthMM, size.HeightMM},
				{100, 100},
				{size.HeightMM, size.WidthMM}, // rotated 90°
			} {
				w, h, err := pixelSize(view[0], view[1], size.WidthMM, size.HeightMM, DefaultDPI)
				if err != nil {
					t.Fatalf("%s %s viewBox %v: %v", name, side, view, err)
				}
				dpiX := float64(w) / (size.WidthMM / mmPerInch)
				dpiY := float64(h) / (size.HeightMM / mmPerInch)
				if dpiX < artwork.MinDPI || dpiY < artwork.MinDPI {
					t.Errorf("%s %s viewBox %v: %d × %d px is %.0f × %.0f DPI, below %.0f",
						name, side, view, w, h, dpiX, dpiY, artwork.MinDPI)
				}
			}
		}
	}
}

// Transparent background is what makes -white auto and -gloss artwork do
// something sensible with an SVG, so it has to survive rasterizing.
func TestUncoveredAreaStaysTransparent(t *testing.T) {
	p := write(t, svgFor(100, 100, `<rect x="0" y="0" width="50" height="100" fill="#000000"/>`))
	img, err := Load(p, 50, 50, 300)
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()

	// Left half drawn, right half untouched.
	if _, _, _, a := img.At(b.Min.X+b.Dx()/4, b.Min.Y+b.Dy()/2).RGBA(); a == 0 {
		t.Error("drawn area is transparent")
	}
	if _, _, _, a := img.At(b.Min.X+3*b.Dx()/4, b.Min.Y+b.Dy()/2).RGBA(); a != 0 {
		t.Errorf("undrawn area has alpha %d, want 0", a)
	}
}

// The renderer leaves out anything it cannot draw. Refusing is the only
// honest response: there is no later check that would notice, because a PDF
// of the wrong artwork is a perfectly valid PDF.
func TestUnsupportedFeaturesAreRefused(t *testing.T) {
	for _, c := range []struct{ name, body, wantMsg string }{
		{"text", `<text x="10" y="10">TREMOLO</text>`, "convert text to paths"},
		{"embedded image", `<image href="a.png" width="10" height="10"/>`, "embedded raster"},
		{"clipPath element", `<defs><clipPath id="c"><rect width="5" height="5"/></clipPath></defs>`, "clipping is not applied"},
		{"clip-path attribute", `<rect width="5" height="5" clip-path="url(#c)"/>`, "clipping is not applied"},
		{"filter attribute", `<rect width="5" height="5" filter="url(#f)"/>`, "filter effects"},
		{"mask attribute", `<rect width="5" height="5" mask="url(#m)"/>`, "masking is not applied"},
		{"pattern", `<defs><pattern id="p"/></defs>`, "pattern fills"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := write(t, svgFor(100, 100, c.body))
			_, err := Load(p, 50, 50, 300)
			if err == nil {
				t.Fatalf("%s was accepted; it would have been silently dropped", c.name)
			}
			if !strings.Contains(err.Error(), c.wantMsg) {
				t.Errorf("error does not say what to do about it:\n%v", err)
			}
		})
	}
}

// <text> and <tspan> arrive together in every real file; saying so twice
// would train the user to skim the message.
func TestRepeatedComplaintsAreReportedOnce(t *testing.T) {
	p := write(t, svgFor(100, 100, `<text x="1" y="1"><tspan>A</tspan><tspan>B</tspan></text>`))
	_, err := Load(p, 50, 50, 300)
	if err == nil {
		t.Fatal("text was accepted")
	}
	if n := strings.Count(err.Error(), "convert text to paths"); n != 1 {
		t.Errorf("text complaint appears %d times, want 1:\n%v", n, err)
	}
}

// "none" is how a document turns an inherited clip or filter back off. It
// removes nothing, so refusing it would reject clean files.
func TestExplicitlyDisabledEffectsAreFine(t *testing.T) {
	p := write(t, svgFor(100, 100, `<rect width="100" height="100" fill="#111" clip-path="none" filter="none"/>`))
	if _, err := Load(p, 50, 50, 300); err != nil {
		t.Fatalf("clip-path=none was refused: %v", err)
	}
}

func TestSVGWithNoSizeIsAnError(t *testing.T) {
	p := write(t, `<svg xmlns="http://www.w3.org/2000/svg">`+filled+`</svg>`)
	_, err := Load(p, 50, 50, 300)
	if err == nil {
		t.Fatal("an SVG with no viewBox was accepted")
	}
	if !strings.Contains(err.Error(), "viewBox") {
		t.Errorf("error does not mention viewBox: %v", err)
	}
}

func TestMalformedXMLIsAnError(t *testing.T) {
	p := write(t, `<svg viewBox="0 0 10 10"><rect`)
	if _, err := Load(p, 50, 50, 300); err == nil {
		t.Fatal("truncated XML was accepted")
	}
}

func TestAbsurdResolutionIsRefusedRatherThanAllocated(t *testing.T) {
	if _, _, err := pixelSize(100, 100, 117, 185, 100000); err == nil {
		t.Fatal("a 100000 DPI canvas was accepted")
	}
}

// Artwork wildly out of shape must still come back as a report about its
// shape, not as a failure to allocate a canvas for it.
func TestWildlyWrongShapeIsRenderedNotRefused(t *testing.T) {
	size := sideSize(t, "1590B", enclosure.SideA)

	p := write(t, svgFor(1000, 30, filled))
	img, err := Load(p, size.WidthMM, size.HeightMM, DefaultDPI)
	if err != nil {
		t.Fatalf("a 1000:30 banner could not be rendered: %v", err)
	}
	b := img.Bounds()
	if px := b.Dx() * b.Dy(); px > maxPixels {
		t.Errorf("canvas is %d px, over the %d cap", px, maxPixels)
	}
	if got, want := float64(b.Dx())/float64(b.Dy()), 1000.0/30.0; math.Abs(got-want)/want > 0.01 {
		t.Errorf("capped canvas aspect = %v, want the SVG's %v", got, want)
	}

	rep := artwork.Check(img, enclosure.SideA, size)
	if !containsAny(rep.Problems, "aspect ratio") {
		t.Errorf("problems do not mention the aspect ratio: %v", rep.Problems)
	}
}

func TestBadArgumentsAreRejected(t *testing.T) {
	p := write(t, svgFor(100, 100, filled))
	if _, err := Load(p, 0, 50, 300); err == nil {
		t.Error("a zero-width artboard was accepted")
	}
	if _, err := Load(p, 50, 50, 0); err == nil {
		t.Error("a zero dpi was accepted")
	}
}

func TestMissingFileReportsThePath(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.svg"), 50, 50, 300)
	if err == nil {
		t.Fatal("a missing file was accepted")
	}
	if !strings.Contains(err.Error(), "nope.svg") {
		t.Errorf("error does not name the file: %v", err)
	}
}

func TestExactRatioReducesToLowestTerms(t *testing.T) {
	// 56 × 108.50 mm reduces to 16:31.
	w, h, ok := exactRatio(56, 108.50, 1322.8, 2563.0)
	if !ok {
		t.Fatal("no exact canvas for a 1590B face")
	}
	if w%16 != 0 || h%31 != 0 || w/16 != h/31 {
		t.Errorf("%d × %d is not a whole multiple of 16:31", w, h)
	}
	if float64(w) < 1322.8 || float64(h) < 2563.0 {
		t.Errorf("%d × %d px is below the requested resolution", w, h)
	}
}

func TestExactRatioDeclinesWhenItWouldBeHuge(t *testing.T) {
	// Two large coprime dimensions: an exact canvas would dwarf the request.
	if _, _, ok := exactRatio(99.97, 100.03, 100, 100); ok {
		t.Error("accepted a canvas far larger than asked for")
	}
}

func sideSize(t *testing.T, name string, side enclosure.Side) enclosure.Size {
	t.Helper()
	e, err := enclosure.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	size, err := e.Size(side)
	if err != nil {
		t.Fatal(err)
	}
	return size
}

func containsAny(msgs []string, sub string) bool {
	for _, m := range msgs {
		if strings.Contains(m, sub) {
			return true
		}
	}
	return false
}
