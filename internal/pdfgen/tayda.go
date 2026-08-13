// Package pdfgen turns a decoded image into a PDF that meets the Tayda UV
// printing file specification.
//
// The rules it implements, from the guide (V2, April 22 2026):
//
//   - The page is exactly the artboard size for the enclosure side, in mm.
//   - Colour is DeviceCMYK. No RGB reaches the file.
//   - Artwork lives in an optional content group (layer) named CMYK.
//   - White ink is a Separation colour named exactly RDG_WHITE, on a layer of
//     the same name, painted before the CMYK artwork so it acts as the
//     undercoat the default White → CMYK → Gloss print order expects.
//   - Nothing is drawn outside the page box.
package pdfgen

import (
	"fmt"
	"image"
	"image/color"
	"io"
	"strconv"
	"strings"
)

// Spot colour names required by the guide. They are case-sensitive: the guide
// is explicit that "Rdg_White" or "rdg_white" will not work.
const (
	SpotWhite = "RDG_WHITE"
	SpotGloss = "RDG_GLOSS" // paid add-on; not generated yet, see README
)

// LayerCMYK is the layer name the guide asks for around colour artwork.
const LayerCMYK = "CMYK"

// Alternate-space CMYK values the guide specifies for each spot swatch. They
// only affect on-screen preview; the printer keys off the spot name.
var (
	whiteAlternate = [4]float64{0.25, 0.25, 0.25, 0.25}
	glossAlternate = [4]float64{0.50, 0.25, 0.25, 0.00}
)

// WhiteMode selects how the RDG_WHITE undercoat is generated.
type WhiteMode int

const (
	// WhiteNone emits no white layer. Correct for white enclosures, where
	// the guide notes all colours show up without an undercoat.
	WhiteNone WhiteMode = iota
	// WhiteAuto follows the artwork: transparent pixels get no white, opaque
	// pixels get full white. This is what you want on a dark enclosure.
	WhiteAuto
	// WhiteFull floods the whole artboard with white regardless of the
	// artwork's transparency.
	WhiteFull
)

func (m WhiteMode) String() string {
	switch m {
	case WhiteNone:
		return "none"
	case WhiteAuto:
		return "auto"
	case WhiteFull:
		return "full"
	}
	return "unknown"
}

// ParseWhiteMode maps a command-line value to a WhiteMode.
func ParseWhiteMode(s string) (WhiteMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "none":
		return WhiteNone, nil
	case "auto", "":
		return WhiteAuto, nil
	case "full":
		return WhiteFull, nil
	}
	return 0, fmt.Errorf("unknown white mode %q (want none, auto or full)", s)
}

// Job is one artboard to render.
type Job struct {
	Image    image.Image
	WidthMM  float64
	HeightMM float64
	White    WhiteMode
}

const mmPerInch = 25.4

func mmToPt(mm float64) float64 { return mm * 72.0 / mmPerInch }

// Build writes the PDF for a job.
func Build(j Job, out io.Writer) error {
	if j.Image == nil {
		return fmt.Errorf("no image")
	}
	b := j.Image.Bounds()
	if b.Dx() == 0 || b.Dy() == 0 {
		return fmt.Errorf("image has zero width or height")
	}
	if j.WidthMM <= 0 || j.HeightMM <= 0 {
		return fmt.Errorf("artboard must have a positive size, got %g × %g mm", j.WidthMM, j.HeightMM)
	}

	cmyk, alpha, transparent := encodeCMYK(j.Image)

	w := newObjWriter()
	catalog := w.reserve()
	pages := w.reserve()
	page := w.reserve()
	content := w.reserve()
	imgCMYK := w.reserve()

	var smask int
	if transparent {
		smask = w.reserve()
	}

	ocgCMYK := w.reserve()

	// Decide the shape of the white layer before reserving its objects.
	whiteAsImage := j.White == WhiteAuto && transparent
	whiteAsRect := j.White == WhiteFull || (j.White == WhiteAuto && !transparent)

	var ocgWhite, sepWhite, imgWhite int
	if whiteAsImage || whiteAsRect {
		ocgWhite = w.reserve()
		sepWhite = w.reserve()
		if whiteAsImage {
			imgWhite = w.reserve()
		}
	}

	pageW := mmToPt(j.WidthMM)
	pageH := mmToPt(j.HeightMM)

	// --- content stream -------------------------------------------------
	// White is painted first: the default print mode lays white down, then
	// CMYK on top of it.
	var cs strings.Builder
	if whiteAsRect {
		fmt.Fprintf(&cs, "/OC /OCw BDC\nq\n/CsW cs\n1 scn\n0 0 %s %s re\nf\nQ\nEMC\n",
			ftoa(pageW), ftoa(pageH))
	} else if whiteAsImage {
		fmt.Fprintf(&cs, "/OC /OCw BDC\nq\n%s 0 0 %s 0 0 cm\n/ImW Do\nQ\nEMC\n",
			ftoa(pageW), ftoa(pageH))
	}
	fmt.Fprintf(&cs, "/OC /OCc BDC\nq\n%s 0 0 %s 0 0 cm\n/Im0 Do\nQ\nEMC\n",
		ftoa(pageW), ftoa(pageH))

	// --- resources ------------------------------------------------------
	xobjects := fmt.Sprintf("/Im0 %d 0 R", imgCMYK)
	if whiteAsImage {
		xobjects += fmt.Sprintf(" /ImW %d 0 R", imgWhite)
	}
	properties := fmt.Sprintf("/OCc %d 0 R", ocgCMYK)
	if ocgWhite != 0 {
		properties += fmt.Sprintf(" /OCw %d 0 R", ocgWhite)
	}
	resources := fmt.Sprintf("<</XObject<<%s>>/Properties<<%s>>", xobjects, properties)
	if sepWhite != 0 {
		resources += fmt.Sprintf("/ColorSpace<</CsW %d 0 R>>", sepWhite)
	}
	resources += ">>"

	// --- objects --------------------------------------------------------
	ocgs := fmt.Sprintf("%d 0 R", ocgCMYK)
	order := ocgs
	if ocgWhite != 0 {
		ocgs = fmt.Sprintf("%d 0 R %d 0 R", ocgWhite, ocgCMYK)
		// Listed in print order: white under, CMYK over.
		order = ocgs
	}
	w.put(catalog, fmt.Sprintf(
		"<</Type/Catalog/Pages %d 0 R/OCProperties<</OCGs[%s]/D<</Order[%s]/ON[%s]>>>>>>",
		pages, ocgs, order, ocgs))

	w.put(pages, fmt.Sprintf("<</Type/Pages/Kids[%d 0 R]/Count 1>>", page))

	w.put(page, fmt.Sprintf(
		"<</Type/Page/Parent %d 0 R/MediaBox[0 0 %s %s]/Resources%s/Contents %d 0 R>>",
		pages, ftoa(pageW), ftoa(pageH), resources, content))

	w.putStream(content, "", []byte(cs.String()))

	imgDict := fmt.Sprintf(
		"/Type/XObject/Subtype/Image/Width %d/Height %d/ColorSpace/DeviceCMYK/BitsPerComponent 8",
		b.Dx(), b.Dy())
	if transparent {
		imgDict += fmt.Sprintf("/SMask %d 0 R", smask)
	}
	w.putStream(imgCMYK, imgDict, cmyk)

	if transparent {
		w.putStream(smask, fmt.Sprintf(
			"/Type/XObject/Subtype/Image/Width %d/Height %d/ColorSpace/DeviceGray/BitsPerComponent 8",
			b.Dx(), b.Dy()), alpha)
	}

	w.put(ocgCMYK, fmt.Sprintf("<</Type/OCG/Name(%s)>>", LayerCMYK))

	if ocgWhite != 0 {
		w.put(ocgWhite, fmt.Sprintf("<</Type/OCG/Name(%s)>>", SpotWhite))
		w.put(sepWhite, separationColorSpace(SpotWhite, whiteAlternate))
	}
	if whiteAsImage {
		// One component per pixel in the Separation space: the sample value
		// is the ink tint, so the artwork's own alpha becomes white coverage.
		w.putStream(imgWhite, fmt.Sprintf(
			"/Type/XObject/Subtype/Image/Width %d/Height %d/ColorSpace %d 0 R/BitsPerComponent 8",
			b.Dx(), b.Dy(), sepWhite), alpha)
	}

	return w.finish(catalog, out)
}

// separationColorSpace builds a Separation colour space whose tint transform
// ramps from no ink to the alternate CMYK values the guide specifies.
func separationColorSpace(name string, alt [4]float64) string {
	return fmt.Sprintf("[/Separation/%s/DeviceCMYK<</FunctionType 2/Domain[0 1]/C0[0 0 0 0]/C1[%s %s %s %s]/N 1>>]",
		name, ftoa(alt[0]), ftoa(alt[1]), ftoa(alt[2]), ftoa(alt[3]))
}

// encodeCMYK converts an image to 8-bit DeviceCMYK samples plus a matching
// 8-bit grey alpha channel. transparent reports whether any pixel is not
// fully opaque, which decides whether the alpha is worth embedding.
func encodeCMYK(img image.Image) (cmyk, alpha []byte, transparent bool) {
	b := img.Bounds()
	n := b.Dx() * b.Dy()
	cmyk = make([]byte, 0, n*4)
	alpha = make([]byte, 0, n)

	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			// RGBA returns alpha-premultiplied components. Undo that, or
			// semi-transparent artwork converts to a muddied colour.
			if a > 0 && a < 0xffff {
				r = r * 0xffff / a
				g = g * 0xffff / a
				bl = bl * 0xffff / a
			}
			c, m, yy, k := color.RGBToCMYK(uint8(r>>8), uint8(g>>8), uint8(bl>>8))
			cmyk = append(cmyk, c, m, yy, k)

			av := uint8(a >> 8)
			alpha = append(alpha, av)
			if av != 0xff {
				transparent = true
			}
		}
	}
	return cmyk, alpha, transparent
}

// ftoa formats a number for the PDF content stream: fixed notation, no
// exponent, no trailing zero noise.
func ftoa(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
