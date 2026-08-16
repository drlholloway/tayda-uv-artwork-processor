package pdfinspect

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// The rule this package is built on is that it never reports "clean" about a
// file it did not read. These are the cases where it did.

// RGB rarely arrives as /DeviceRGB. Illustrator, Inkscape and Acrobat all
// write [/ICCBased N 0 R], and only the component count in the profile says
// which space it is — so an entirely RGB file was passing as "no RGB".
func TestICCBasedRGBIsRGB(t *testing.T) {
	for _, c := range []struct {
		n       int
		want    string
		wantRGB bool
	}{
		{3, "ICCBased RGB", true},
		{4, "ICCBased CMYK", false},
		{1, "ICCBased Gray", false},
	} {
		t.Run(c.want, func(t *testing.T) {
			b := pdf(
				"<</Type/Catalog/Pages 2 0 R>>",
				"<</Type/Pages/Kids[3 0 R]/Count 1>>",
				"<</Type/Page/Parent 2 0 R/MediaBox[0 0 158.74 307.56]"+
					"/Resources<</ColorSpace<</Cs0[/ICCBased 5 0 R]>>>>/Contents 4 0 R>>",
				streamObj(t, "", []byte("q Q\n")),
				streamObj(t, fmt.Sprintf("/N %d", c.n), []byte("profile")),
			)
			r, err := Inspect(b)
			if err != nil {
				t.Fatal(err)
			}
			if !contains(r.ColorSpaces, c.want) {
				t.Errorf("colour spaces %q do not name %q", r.ColorSpaces, c.want)
			}
			if r.HasRGB() != c.wantRGB {
				t.Errorf("HasRGB() = %v, want %v for a %d-component profile", r.HasRGB(), c.wantRGB, c.n)
			}
		})
	}
}

// An ICCBased space whose profile cannot be read says nothing about itself,
// and saying nothing at all would be a clean verdict on an unknown.
func TestUnreadableICCProfileIsReported(t *testing.T) {
	b := pdf(
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 158.74 307.56]"+
			"/Resources<</ColorSpace<</Cs0[/ICCBased 9 0 R]>>>>/Contents 4 0 R>>",
		streamObj(t, "", []byte("q Q\n")),
	)
	r, err := Inspect(b)
	if err != nil {
		t.Fatal(err)
	}
	if !notesMention(r, "ICCBased") {
		t.Errorf("an unresolvable ICC profile went unmentioned: %q", r.Notes)
	}
}

// A stream that cannot be decoded is where a stray "/DeviceRGB cs" would sit
// unseen, so skipping it quietly turns "no RGB" into a claim about bytes that
// were never read.
func TestUndecodableStreamIsReported(t *testing.T) {
	b := pdf(
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 158.74 307.56]/Contents 4 0 R>>",
		streamObj(t, "", []byte("q Q\n")),
		"<</Filter/LZWDecode/Length 4>>\nstream\nabcd\nendstream",
	)
	r, err := Inspect(b)
	if err != nil {
		t.Fatal(err)
	}
	if !notesMention(r, "could not be decoded") {
		t.Errorf("an undecodable stream went unmentioned: %q", r.Notes)
	}
}

// The predictor guard only looked at the dictionary form, but the array form
// is what writers emit whenever /Filter is an array.
func TestPredictorIsCaughtInEitherForm(t *testing.T) {
	for _, c := range []struct{ name, parms string }{
		{"dictionary", "/DecodeParms<</Predictor 12/Columns 5>>"},
		{"array", "/DecodeParms[<</Predictor 12/Columns 5>>]"},
		{"abbreviated", "/DP<</Predictor 12/Columns 5>>"},
	} {
		t.Run(c.name, func(t *testing.T) {
			z := deflate(t, []byte("q /DeviceRGB cs Q\n"))
			b := pdf(
				"<</Type/Catalog/Pages 2 0 R>>",
				"<</Type/Pages/Kids[3 0 R]/Count 1>>",
				"<</Type/Page/Parent 2 0 R/MediaBox[0 0 158.74 307.56]/Contents 4 0 R>>",
				fmt.Sprintf("<</Filter/FlateDecode%s/Length %d>>\nstream\n%s\nendstream",
					c.parms, len(z), z),
			)
			r, err := Inspect(b)
			if err != nil {
				return // refusing outright is also honest
			}
			if !notesMention(r, "predictor") {
				t.Errorf("predictor-encoded bytes passed as decoded: notes %q, order %q",
					r.Notes, r.PaintOrder)
			}
		})
	}
}

// Dropping an unreadable /Contents entry hands back a short content stream
// that reads as complete: the paint order comes back missing whatever lived
// in the dropped part, and a missing CMYK layer reads as a clean order.
func TestUnreadableContentsEntryIsNotSilentlySkipped(t *testing.T) {
	b := pdf(
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 158.74 307.56]"+
			"/Resources<</Properties<</OC0 5 0 R>>>>/Contents[4 0 R 9 0 R]>>",
		streamObj(t, "", []byte("/OC /OC0 BDC EMC\n")),
		"<</Type/OCG/Name(RDG_WHITE)>>",
	)
	r, err := Inspect(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.PaintOrder) > 0 && len(r.Notes) == 0 {
		t.Errorf("a truncated content stream produced order %q with no note", r.PaintOrder)
	}
}

// /Rotate turns the page, so a quarter turn swaps what the artboard measures.
// Reported unrotated, a 56 × 108.5 mm page that prints as 108.5 × 56 gets
// matched to the wrong enclosure side with full confidence.
func TestRotatedPageReportsTheArtboardAsItPrints(t *testing.T) {
	for _, c := range []struct {
		rotate     string
		wantW      float64
		wantH      float64
		descriptor string
	}{
		{"0", 56, 108.5, "upright"},
		{"90", 108.5, 56, "quarter turn"},
		{"270", 108.5, 56, "three quarter turn"},
		{"180", 56, 108.5, "half turn"},
		{"-90", 108.5, 56, "negative quarter turn"},
	} {
		t.Run(c.descriptor, func(t *testing.T) {
			b := pdf(
				"<</Type/Catalog/Pages 2 0 R>>",
				"<</Type/Pages/Kids[3 0 R]/Count 1>>",
				"<</Type/Page/Parent 2 0 R/MediaBox[0 0 158.74 307.56]/Rotate "+c.rotate+"/Contents 4 0 R>>",
				streamObj(t, "", []byte("q Q\n")),
			)
			r, err := Inspect(b)
			if err != nil {
				t.Fatal(err)
			}
			if !closeTo(r.WidthMM, c.wantW) || !closeTo(r.HeightMM, c.wantH) {
				t.Errorf("/Rotate %s gives %.1f × %.1f mm, want %.1f × %.1f",
					c.rotate, r.WidthMM, r.HeightMM, c.wantW, c.wantH)
			}
		})
	}
}

// The /Properties keys come through the parser decoded while the content
// stream token does not, so without decoding the token too the lookup never
// matches and the order check compares against a name no layer has.
func TestLayerNameFromTheContentStreamIsDecoded(t *testing.T) {
	b := pdf(
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 158.74 307.56]"+
			"/Resources<</Properties<</RDG#5FW 5 0 R>>>>/Contents 4 0 R>>",
		streamObj(t, "", []byte("/OC /RDG#5FW BDC EMC\n")),
		"<</Type/OCG/Name(RDG_WHITE)>>",
	)
	r, err := Inspect(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.PaintOrder) != 1 || r.PaintOrder[0] != "RDG_WHITE" {
		t.Errorf("paint order = %q, want [RDG_WHITE] resolved through the escaped resource name", r.PaintOrder)
	}
}

// A damaged file is the one this package promises it can still look at, so it
// must not be the one that takes the tool down. Each of these used to panic
// or hang; a panic also aborts a whole `inspect *.pdf` run on one bad file.
func TestDamagedFilesAreReportedNotFatal(t *testing.T) {
	for _, c := range []struct{ name, body string }{
		{"negative /First", "<</Type/ObjStm/N 1/First -1/Length 10>>\nstream\n0123456789\nendstream"},
		{"enormous /First", "<</Type/ObjStm/N 1/First 1e20/Length 10>>\nstream\n0123456789\nendstream"},
		{"negative /N", "<</Type/ObjStm/N -5/First 2/Length 10>>\nstream\n0123456789\nendstream"},
		{"enormous /Length", "<</Length 1e20>>\nstream\nabcd\nendstream"},
		{"negative /Length", "<</Length -1>>\nstream\nabcd\nendstream"},
	} {
		t.Run(c.name, func(t *testing.T) {
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer func() {
					if p := recover(); p != nil {
						t.Errorf("panicked instead of reporting: %v", p)
					}
				}()
				// The verdict does not matter; not crashing does.
				_, _ = Inspect(pdf("<</Type/Catalog/Pages 2 0 R>>", c.body))
			}()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("Inspect did not return")
			}
		})
	}
}

// A /Kids entry pointing back at its own node doubles the work at every
// level, so the depth cap alone leaves a tree thirty deep at a billion
// visits. Bounding depth is not the same as bounding work.
func TestCyclicPageTreeTerminates(t *testing.T) {
	b := pdf(
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[2 0 R 2 0 R 3 0 R]/Count 1>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 158.74 307.56]/Contents 4 0 R>>",
		streamObj(t, "", []byte("q Q\n")),
	)

	done := make(chan Result, 1)
	go func() {
		r, _ := Inspect(b)
		done <- r
	}()
	select {
	case r := <-done:
		if r.Pages != 1 {
			t.Errorf("a self-referencing node gives %d pages, want the 1 real one", r.Pages)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Inspect did not return on a cyclic page tree")
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func notesMention(r Result, sub string) bool {
	for _, n := range r.Notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

func closeTo(got, want float64) bool {
	d := got - want
	return d < 0.05 && d > -0.05
}
