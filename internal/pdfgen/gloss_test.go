package pdfgen

import (
	"bytes"
	"compress/zlib"
	"image"
	"image/color"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// grayMask is an opaque greyscale mask: v=255 coats, v=0 leaves bare.
func grayMask(w, h int, v uint8) image.Image {
	m := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m.SetGray(x, y, color.Gray{Y: v})
		}
	}
	return m
}

// alphaMask is a shape on a transparent background: the left half is coated.
func alphaMask(w, h int) image.Image {
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a := uint8(0)
			if x < w/2 {
				a = 255
			}
			m.Set(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: a})
		}
	}
	return m
}

// --- reading the finished file back ------------------------------------

func contentStream(t *testing.T, pdf []byte) []byte {
	t.Helper()
	m := regexp.MustCompile(`/Contents (\d+) 0 R`).FindSubmatch(pdf)
	if m == nil {
		t.Fatal("page has no /Contents")
	}
	head := regexp.MustCompile(`(?s)\n` + string(m[1]) + ` 0 obj\n<<[^>]*/Length (\d+)>>\nstream\n`)
	loc := head.FindSubmatchIndex(pdf)
	if loc == nil {
		t.Fatalf("content object %s not found", m[1])
	}
	n, err := strconv.Atoi(string(pdf[loc[2]:loc[3]]))
	if err != nil {
		t.Fatalf("bad /Length: %v", err)
	}
	zr, err := zlib.NewReader(bytes.NewReader(pdf[loc[1] : loc[1]+n]))
	if err != nil {
		t.Fatalf("content stream is not valid deflate data: %v", err)
	}
	data, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("content stream: %v", err)
	}
	return data
}

// resourceToLayer maps the short resource names used in the content stream
// (/OCc, /OC0 …) to the layer names a printer would see.
func resourceToLayer(t *testing.T, pdf []byte) map[string]string {
	t.Helper()
	byObj := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^(\d+) 0 obj\n<</Type/OCG/Name\(([^)]+)\)>>`).FindAllSubmatch(pdf, -1) {
		byObj[string(m[1])] = string(m[2])
	}
	props := regexp.MustCompile(`/Properties<<([^>]*)>>`).FindSubmatch(pdf)
	if props == nil {
		t.Fatal("page has no /Properties")
	}
	out := map[string]string{}
	for _, p := range regexp.MustCompile(`/(\w+) (\d+) 0 R`).FindAllSubmatch(props[1], -1) {
		out[string(p[1])] = byObj[string(p[2])]
	}
	return out
}

// paintOrder returns the layer names in the order the file actually paints
// them.
func paintOrder(t *testing.T, pdf []byte) []string {
	t.Helper()
	res := resourceToLayer(t, pdf)
	var out []string
	for _, m := range regexp.MustCompile(`/OC /(\w+) BDC`).FindAllSubmatch(contentStream(t, pdf), -1) {
		out = append(out, res[string(m[1])])
	}
	return out
}

// --- tests --------------------------------------------------------------

// The guide's checklist calls the White → CMYK → Gloss order VERY IMPORTANT.
// Read it back out of the finished file rather than trusting the builder.
func TestPrintOrderIsWhiteThenCMYKThenGloss(t *testing.T) {
	cases := []struct {
		name string
		job  Job
		want []string
	}{
		{name: "all three layers",
			job:  Job{White: WhiteFull, Gloss: GlossFull},
			want: []string{SpotWhite, LayerCMYK, SpotGloss}},
		{name: "gloss without white",
			job:  Job{White: WhiteNone, Gloss: GlossFull},
			want: []string{LayerCMYK, SpotGloss}},
		{name: "white without gloss",
			job:  Job{White: WhiteFull, Gloss: GlossNone},
			want: []string{SpotWhite, LayerCMYK}},
		{name: "artwork only",
			job:  Job{White: WhiteNone, Gloss: GlossNone},
			want: []string{LayerCMYK}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := tc.job
			j.Image = opaqueImage(8, 8)
			j.WidthMM, j.HeightMM = 56, 108.5
			got := paintOrder(t, build(t, j))
			if len(got) != len(tc.want) {
				t.Fatalf("paint order = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("paint order = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// Order must hold when the layers are coverage images rather than rectangles.
func TestPrintOrderWithCoverageImages(t *testing.T) {
	j := Job{
		Image: transparentImage(8, 8), WidthMM: 56, HeightMM: 108.5,
		White: WhiteAuto, Gloss: GlossMask, GlossMask: alphaMask(8, 8),
	}
	got := paintOrder(t, build(t, j))
	want := []string{SpotWhite, LayerCMYK, SpotGloss}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("paint order = %v, want %v", got, want)
		}
	}
}

func TestGlossNoneOmitsTheLayer(t *testing.T) {
	pdf := build(t, Job{Image: opaqueImage(6, 6), WidthMM: 35, HeightMM: 89, White: WhiteFull})
	if bytes.Contains(pdf, []byte(SpotGloss)) {
		t.Error("gloss is a paid add-on and must not appear unless asked for")
	}
}

func TestGlossSpotColourMatchesTheGuide(t *testing.T) {
	pdf := build(t, Job{Image: opaqueImage(6, 6), WidthMM: 35, HeightMM: 89, Gloss: GlossFull})
	// The guide specifies RDG_GLOSS as a spot colour with alternate CMYK
	// values 50/25/25/0.
	want := "[/Separation/RDG_GLOSS/DeviceCMYK<</FunctionType 2/Domain[0 1]/C0[0 0 0 0]/C1[0.5 0.25 0.25 0]/N 1>>]"
	if !bytes.Contains(pdf, []byte(want)) {
		t.Errorf("missing or wrong gloss separation, want %s", want)
	}
	if !bytes.Contains(pdf, []byte("/Type/OCG/Name(RDG_GLOSS)")) {
		t.Error("missing RDG_GLOSS layer")
	}
}

func TestGlossMaskIsRequiredForMaskMode(t *testing.T) {
	err := Build(Job{Image: opaqueImage(4, 4), WidthMM: 35, HeightMM: 89, Gloss: GlossMask}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("mask mode without a mask should fail")
	}
}

func TestGlossMaskOfADifferentSizeToTheArtwork(t *testing.T) {
	// The mask is scaled to the artboard independently, so it need not match
	// the artwork's pixel dimensions.
	pdf := build(t, Job{
		Image: opaqueImage(64, 64), WidthMM: 35, HeightMM: 89,
		Gloss: GlossMask, GlossMask: grayMask(16, 40, 255),
	})
	if !bytes.Contains(pdf, []byte("/Width 16/Height 40")) {
		t.Error("gloss coverage image should keep the mask's own dimensions")
	}
	assertXrefValid(t, pdf)
}

func TestGlossAndWhiteCoexist(t *testing.T) {
	pdf := build(t, Job{
		Image: transparentImage(8, 8), WidthMM: 62, HeightMM: 117,
		White: WhiteAuto, Gloss: GlossMask, GlossMask: alphaMask(8, 8),
	})
	for _, want := range []string{"RDG_WHITE", "RDG_GLOSS", "/Type/OCG/Name(CMYK)"} {
		if !bytes.Contains(pdf, []byte(want)) {
			t.Errorf("missing %q", want)
		}
	}
	assertXrefValid(t, pdf)
	if bytes.Contains(pdf, []byte("/DeviceRGB")) {
		t.Error("no RGB may reach the file")
	}
}

// A Separation image paints its alternate space, and zero tint in the
// alternate space is white — not nothing. Without a soft mask tracking the
// tint, the uncoated parts of a layer cover everything beneath them in an
// opaque white slab: a gloss mask hides the artwork entirely.
func TestCoverageLayersPaintNothingWhereThereIsNoInk(t *testing.T) {
	pdf := build(t, Job{
		Image: transparentImage(8, 8), WidthMM: 56, HeightMM: 108.5,
		White: WhiteAuto, Gloss: GlossMask, GlossMask: alphaMask(8, 8),
	})
	// Coverage images are the ones in a Separation space, i.e. an indirect
	// /ColorSpace reference rather than /DeviceCMYK or /DeviceGray.
	images := regexp.MustCompile(`/Subtype/Image[^>]*/ColorSpace \d+ 0 R[^>]*`).FindAllString(string(pdf), -1)
	if len(images) != 2 {
		t.Fatalf("expected a white and a gloss coverage image, found %d", len(images))
	}
	for _, dict := range images {
		if !strings.Contains(dict, "/SMask") {
			t.Errorf("coverage image has no soft mask, so zero tint will paint opaque white: %s", dict)
		}
	}
}

// A white mask coats everything, a black mask coats nothing: getting this
// backwards would varnish exactly the wrong areas.
func TestGlossCoverageReadsAGreyscaleMask(t *testing.T) {
	cases := map[uint8]float64{255: 1.0, 0: 0.0, 128: 128.0 / 255.0}
	for v, want := range cases {
		got := glossCoverage(t, Job{
			Image: opaqueImage(8, 8), WidthMM: 56, HeightMM: 108.5,
			Gloss: GlossMask, GlossMask: grayMask(8, 8, v),
		})
		if math.Abs(got-want) > 0.01 {
			t.Errorf("grey %d mask: coverage = %.3f, want %.3f", v, got, want)
		}
	}
}

func TestGlossCoverageReadsAMaskAlpha(t *testing.T) {
	// Left half opaque, right half transparent.
	got := glossCoverage(t, Job{
		Image: opaqueImage(8, 8), WidthMM: 56, HeightMM: 108.5,
		Gloss: GlossMask, GlossMask: alphaMask(8, 8),
	})
	if math.Abs(got-0.5) > 0.01 {
		t.Errorf("half-transparent mask: coverage = %.3f, want 0.5", got)
	}
}

// The reported coverage has to describe the layer that was written, for every
// mode — not just the one that happens to carry a mask image.
//
// GlossArtwork on opaque artwork is the case that matters. The coating floods
// the whole side, but the artwork read as though it were a mask would fall
// back to luminance and put this at 0.32, because opaqueImage is a dark red.
// Reporting that would understate a full-side varnish, and the guide's
// fingerprint warning turns on this number.
func TestGlossCoverageMatchesTheLayerThatGetsWritten(t *testing.T) {
	for _, c := range []struct {
		name string
		job  Job
		want float64
	}{
		{"none coats nothing", Job{Image: opaqueImage(8, 8), Gloss: GlossNone}, 0},
		{"full floods the side", Job{Image: opaqueImage(8, 8), Gloss: GlossFull}, 1},
		{"artwork on opaque art floods the side", Job{Image: opaqueImage(8, 8), Gloss: GlossArtwork}, 1},
		{"artwork follows the artwork's alpha", Job{Image: transparentImage(8, 8), Gloss: GlossArtwork}, 0.5},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.job.WidthMM, c.job.HeightMM = 56, 108.5
			if got := glossCoverage(t, c.job); math.Abs(got-c.want) > 0.01 {
				t.Errorf("coverage = %.3f, want %.3f", got, c.want)
			}
		})
	}
}

func TestGlossCoverageReportsAMissingMask(t *testing.T) {
	_, err := GlossCoverage(Job{
		Image: opaqueImage(8, 8), WidthMM: 56, HeightMM: 108.5, Gloss: GlossMask,
	})
	if err == nil {
		t.Error("gloss mask mode with no mask reported a coverage anyway")
	}
}

func glossCoverage(t *testing.T, j Job) float64 {
	t.Helper()
	got, err := GlossCoverage(j)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestParseGlossMode(t *testing.T) {
	for in, want := range map[string]GlossMode{
		"none": GlossNone, "": GlossNone, "FULL": GlossFull,
		"artwork": GlossArtwork, "mask": GlossMask,
	} {
		got, err := ParseGlossMode(in)
		if err != nil {
			t.Errorf("ParseGlossMode(%q): %v", in, err)
		} else if got != want {
			t.Errorf("ParseGlossMode(%q) = %v, want %v", in, got, want)
		}
	}
	if _, err := ParseGlossMode("shiny"); err == nil {
		t.Error("expected an error for an unknown mode")
	}
}
