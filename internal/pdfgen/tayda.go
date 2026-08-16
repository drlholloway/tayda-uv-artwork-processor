// Package pdfgen turns a decoded image into a PDF that meets the Tayda UV
// printing file specification.
//
// The rules it implements, from the guide (V2, April 22 2026):
//
//   - The page is exactly the artboard size for the enclosure side, in mm.
//   - Colour is DeviceCMYK. No RGB reaches the file.
//   - Artwork lives in an optional content group (layer) named CMYK.
//   - White ink is a Separation colour named exactly RDG_WHITE, on a layer of
//     the same name, painted before the CMYK artwork.
//   - Varnish is a Separation colour named exactly RDG_GLOSS, on a layer of
//     the same name, painted after the CMYK artwork.
//   - That ordering is the default White → CMYK → Gloss print mode.
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
	SpotGloss = "RDG_GLOSS"
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

// GlossMode selects how the RDG_GLOSS varnish layer is generated.
//
// Gloss is a paid add-on, so it is off unless asked for. Which finish the
// varnish actually is — gloss varnish or gloss matte — is not recorded in the
// PDF; it is chosen in the Tayda Box Tool when the template is saved.
type GlossMode int

const (
	// GlossNone emits no varnish layer.
	GlossNone GlossMode = iota
	// GlossFull varnishes the whole artboard.
	GlossFull
	// GlossArtwork varnishes wherever the artwork is opaque.
	GlossArtwork
	// GlossMask varnishes according to a separate mask image, which is how
	// you coat only part of a design.
	GlossMask
)

func (m GlossMode) String() string {
	switch m {
	case GlossNone:
		return "none"
	case GlossFull:
		return "full"
	case GlossArtwork:
		return "artwork"
	case GlossMask:
		return "mask"
	}
	return "unknown"
}

// ParseGlossMode maps a command-line value to a GlossMode.
func ParseGlossMode(s string) (GlossMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "none", "":
		return GlossNone, nil
	case "full":
		return GlossFull, nil
	case "artwork":
		return GlossArtwork, nil
	case "mask":
		return GlossMask, nil
	}
	return 0, fmt.Errorf("unknown gloss mode %q (want none, full, artwork or mask)", s)
}

// LayerSummary names the layers a job will produce, in the order the printer
// lays them down. Both front ends show it after a conversion.
func LayerSummary(w WhiteMode, g GlossMode) string {
	layers := make([]string, 0, 3)
	if w != WhiteNone {
		layers = append(layers, SpotWhite)
	}
	layers = append(layers, LayerCMYK)
	if g != GlossNone {
		layers = append(layers, SpotGloss)
	}
	return strings.Join(layers, " → ")
}

// Job is one artboard to render.
type Job struct {
	Image    image.Image
	WidthMM  float64
	HeightMM float64
	White    WhiteMode
	Gloss    GlossMode
	// GlossMask supplies varnish coverage when Gloss is GlossMask. Opaque or
	// white areas are coated; transparent or black areas are left bare.
	GlossMask image.Image
}

// coating is an ink layer painted through a Separation colour space: white
// under the artwork, varnish over it. Both are the same shape of object, so
// they share one code path.
type coating struct {
	name      string     // spot colour and layer name, e.g. RDG_WHITE
	alternate [4]float64 // preview-only CMYK values from the guide

	// coverage holds one 8-bit tint per pixel, or nil to flood the whole
	// artboard with a rectangle — cheaper, and identical in result when
	// coverage would be uniformly full.
	coverage []byte
	w, h     int

	// Assigned during Build.
	ocgObj, csObj, imgObj, smaskObj int
	ocName, csName, imName          string
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

	under := whiteCoating(j.White, alpha, transparent, b.Dx(), b.Dy())
	over, err := glossCoating(j.Gloss, j.GlossMask, alpha, transparent, b.Dx(), b.Dy())
	if err != nil {
		return err
	}

	// Print order: white, then artwork, then varnish.
	var coats []*coating
	if under != nil {
		coats = append(coats, under)
	}
	if over != nil {
		coats = append(coats, over)
	}

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

	for i, c := range coats {
		c.ocgObj = w.reserve()
		c.csObj = w.reserve()
		if c.coverage != nil {
			c.imgObj = w.reserve()
			c.smaskObj = w.reserve()
		}
		c.ocName = fmt.Sprintf("OC%d", i)
		c.csName = fmt.Sprintf("Cs%d", i)
		c.imName = fmt.Sprintf("ImC%d", i)
	}

	pageW := mmToPt(j.WidthMM)
	pageH := mmToPt(j.HeightMM)

	// --- content stream -------------------------------------------------
	var cs strings.Builder
	if under != nil {
		writeCoating(&cs, under, pageW, pageH)
	}
	fmt.Fprintf(&cs, "/OC /OCc BDC\nq\n%s 0 0 %s 0 0 cm\n/Im0 Do\nQ\nEMC\n",
		ftoa(pageW), ftoa(pageH))
	if over != nil {
		writeCoating(&cs, over, pageW, pageH)
	}

	// --- resources ------------------------------------------------------
	xobjects := fmt.Sprintf("/Im0 %d 0 R", imgCMYK)
	properties := fmt.Sprintf("/OCc %d 0 R", ocgCMYK)
	var colorspaces string
	for _, c := range coats {
		properties += fmt.Sprintf(" /%s %d 0 R", c.ocName, c.ocgObj)
		colorspaces += fmt.Sprintf("/%s %d 0 R", c.csName, c.csObj)
		if c.imgObj != 0 {
			xobjects += fmt.Sprintf(" /%s %d 0 R", c.imName, c.imgObj)
		}
	}
	resources := fmt.Sprintf("<</XObject<<%s>>/Properties<<%s>>", xobjects, properties)
	if colorspaces != "" {
		resources += "/ColorSpace<<" + colorspaces + ">>"
	}
	resources += ">>"

	// --- objects --------------------------------------------------------
	// Layers are listed in print order so a reader's layer panel matches the
	// order the inks actually go down.
	var refs []string
	if under != nil {
		refs = append(refs, fmt.Sprintf("%d 0 R", under.ocgObj))
	}
	refs = append(refs, fmt.Sprintf("%d 0 R", ocgCMYK))
	if over != nil {
		refs = append(refs, fmt.Sprintf("%d 0 R", over.ocgObj))
	}
	ocgList := strings.Join(refs, " ")

	w.put(catalog, fmt.Sprintf(
		"<</Type/Catalog/Pages %d 0 R/OCProperties<</OCGs[%s]/D<</Order[%s]/ON[%s]>>>>>>",
		pages, ocgList, ocgList, ocgList))

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

	for _, c := range coats {
		w.put(c.ocgObj, fmt.Sprintf("<</Type/OCG/Name(%s)>>", c.name))
		w.put(c.csObj, separationColorSpace(c.name, c.alternate))
		if c.imgObj != 0 {
			// One component per pixel in the Separation space: the sample
			// value is the ink tint, so the coverage map drives the ink.
			//
			// The same map is also the soft mask. Without it, a tint of zero
			// paints the alternate space's zero — which is white, not
			// nothing — and the layer covers the artwork beneath it with an
			// opaque white slab. Alpha must track tint so that "no ink"
			// means no ink.
			w.putStream(c.imgObj, fmt.Sprintf(
				"/Type/XObject/Subtype/Image/Width %d/Height %d/ColorSpace %d 0 R/BitsPerComponent 8/SMask %d 0 R",
				c.w, c.h, c.csObj, c.smaskObj), c.coverage)
			w.putStream(c.smaskObj, fmt.Sprintf(
				"/Type/XObject/Subtype/Image/Width %d/Height %d/ColorSpace/DeviceGray/BitsPerComponent 8",
				c.w, c.h), c.coverage)
		}
	}

	return w.finish(catalog, out)
}

// writeCoating paints one ink layer across the page, either as a flood-filled
// rectangle or as a coverage image.
func writeCoating(sb *strings.Builder, c *coating, pageW, pageH float64) {
	fmt.Fprintf(sb, "/OC /%s BDC\nq\n", c.ocName)
	if c.coverage == nil {
		fmt.Fprintf(sb, "/%s cs\n1 scn\n0 0 %s %s re\nf\n", c.csName, ftoa(pageW), ftoa(pageH))
	} else {
		fmt.Fprintf(sb, "%s 0 0 %s 0 0 cm\n/%s Do\n", ftoa(pageW), ftoa(pageH), c.imName)
	}
	sb.WriteString("Q\nEMC\n")
}

func whiteCoating(mode WhiteMode, alpha []byte, transparent bool, w, h int) *coating {
	c := &coating{name: SpotWhite, alternate: whiteAlternate, w: w, h: h}
	switch mode {
	case WhiteNone:
		return nil
	case WhiteFull:
		return c // flood
	case WhiteAuto:
		if transparent {
			c.coverage = alpha
		}
		return c
	}
	return nil
}

func glossCoating(mode GlossMode, mask image.Image, alpha []byte, transparent bool, w, h int) (*coating, error) {
	c := &coating{name: SpotGloss, alternate: glossAlternate, w: w, h: h}
	switch mode {
	case GlossNone:
		return nil, nil
	case GlossFull:
		return c, nil
	case GlossArtwork:
		if transparent {
			c.coverage = alpha
		}
		return c, nil
	case GlossMask:
		if mask == nil {
			return nil, fmt.Errorf("gloss mode is mask but no mask image was supplied")
		}
		b := mask.Bounds()
		if b.Dx() == 0 || b.Dy() == 0 {
			return nil, fmt.Errorf("gloss mask has zero width or height")
		}
		c.coverage, c.w, c.h = coverageFrom(mask)
		return c, nil
	}
	return nil, fmt.Errorf("unknown gloss mode %v", mode)
}

// coverageFrom reads an image as a coverage map of 8-bit ink tints.
//
// If the image has any transparency its alpha channel is the coverage, so a
// shape on a transparent background works as a mask directly. Otherwise
// luminance is used, so a plain greyscale mask works too: white coats, black
// leaves bare.
func coverageFrom(img image.Image) (cov []byte, w, h int) {
	b := img.Bounds()
	w, h = b.Dx(), b.Dy()
	alpha := make([]byte, 0, w*h)
	luma := make([]byte, 0, w*h)
	transparent := false

	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			if a > 0 && a < 0xffff {
				r = r * 0xffff / a
				g = g * 0xffff / a
				bl = bl * 0xffff / a
			}
			av := uint8(a >> 8)
			alpha = append(alpha, av)
			if av != 0xff {
				transparent = true
			}
			// Rec. 601 luma, which tracks perceived brightness closely
			// enough for deciding where varnish lands.
			luma = append(luma, uint8((299*(r>>8)+587*(g>>8)+114*(bl>>8))/1000))
		}
	}
	if transparent {
		return alpha, w, h
	}
	return luma, w, h
}

// GlossCoverage reports how much of the artboard the varnish layer will
// actually cover, from 0 (none) to 1 (all of it). The guide warns that large
// areas of gloss varnish attract fingerprints and add days to production, so
// callers say so before an order goes out.
//
// It builds the same coating Build does rather than measuring an image
// directly, because for GlossArtwork the two do not agree. The coating
// follows the artwork's alpha, and floods the side when the artwork is
// opaque; reading that same artwork as though it were a mask would fall back
// to luminance and report a dark design as barely varnished when in fact the
// whole side is being coated. Asking the builder is the only way for the
// number to describe the file that was written.
func GlossCoverage(j Job) (float64, error) {
	if j.Gloss == GlossNone {
		return 0, nil
	}

	var (
		alpha       []byte
		transparent bool
		w, h        int
	)
	if j.Image != nil {
		b := j.Image.Bounds()
		w, h = b.Dx(), b.Dy()
		// Only GlossArtwork reads the alpha: GlossFull floods without looking
		// and GlossMask takes its coverage from the mask. Converting for the
		// other two would repeat the whole CMYK pass Build has just done and
		// throw away a buffer four times the size of the image — up to a
		// quarter of a gigabyte on a large artboard — to reach a constant.
		if j.Gloss == GlossArtwork {
			_, alpha, transparent = encodeCMYK(j.Image)
		}
	}

	c, err := glossCoating(j.Gloss, j.GlossMask, alpha, transparent, w, h)
	if err != nil {
		return 0, err
	}
	switch {
	case c == nil:
		return 0, nil
	case c.coverage == nil:
		return 1, nil // a flood-filled rectangle covers the whole side
	case len(c.coverage) == 0:
		return 0, nil
	}
	return coverageFraction(c.coverage), nil
}

// coverageFraction averages a coverage map, where each sample is an 8-bit ink
// tint.
func coverageFraction(cov []byte) float64 {
	var total uint64
	for _, v := range cov {
		total += uint64(v)
	}
	return float64(total) / float64(len(cov)) / 255
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
