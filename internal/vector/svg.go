// Package vector rasterizes vector artwork so the rest of the pipeline can
// treat it as an ordinary image.
//
// It sits beside the stdlib-only core rather than inside it. `enclosure`,
// `artwork` and `pdfgen` do not import this package, so the code that knows
// how to produce a printable file still depends on nothing but the Go
// toolchain; only the callers that choose a source file reach in here. A
// rasterized SVG is an `image.Image` like any other, and everything
// downstream — validation, CMYK conversion, the white and gloss coatings —
// runs unchanged.
//
// Rasterizing at the door rather than carrying paths into the PDF is a
// deliberate limit. Tayda prints the file exactly as submitted and nobody
// looks at it first, so a translation bug is invisible until an enclosure
// comes back wrong. Pixels are dull, but they are wrong in ways you can see.
package vector

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"image"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

// DefaultDPI is the resolution SVG artwork rasterizes at.
//
// Well above the 300 DPI floor the guide states, because here the pixels are
// generated rather than supplied: there is no reason to sit near the minimum,
// and 600 DPI keeps hairlines and small lettering clean at pedal-face sizes
// without producing an unreasonable file.
const DefaultDPI = 600

// maxPixels caps the raster. The largest enclosure side is 12 megapixels at
// DefaultDPI and 48 at 1200 DPI, so this leaves room for wanting more
// resolution while keeping the RGBA buffer inside a few hundred megabytes.
const maxPixels = 64 << 20

const mmPerInch = 25.4

// aspectSnapPct is how far an SVG's own aspect ratio may sit from the
// artboard's before the two stop counting as the same shape.
//
// It exists because an SVG's aspect ratio is written in decimal and the
// artboard's is a ratio of millimetres, so artwork drawn deliberately to fit
// can miss by a rounding place or two in the file. The threshold is far below
// anything physical — 0.1% of a 60 mm side is 0.06 mm, an order of magnitude
// inside Tayda's own ±0.5 mm registration tolerance — while real drift, the
// kind that stretches artwork visibly, is orders of magnitude larger and
// still reaches artwork.Check intact.
const aspectSnapPct = 0.1

// IsVector reports whether path names a format this package rasterizes.
func IsVector(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".svg")
}

// Load rasterizes an SVG onto a canvas sized for the given artboard.
//
// The canvas keeps the SVG's own aspect ratio rather than being forced to the
// artboard's. Rendering straight to the artboard's proportions would stretch
// mis-shaped artwork silently and, worse, would leave artwork.Check measuring
// a shape this package had just manufactured — every file would report a
// perfect fit. Sizing from the SVG instead means the drift is still the
// artwork's own, and still gets reported.
func Load(path string, targetWidthMM, targetHeightMM, dpi float64) (image.Image, error) {
	if targetWidthMM <= 0 || targetHeightMM <= 0 {
		return nil, fmt.Errorf("artboard must have a positive size, got %g × %g mm", targetWidthMM, targetHeightMM)
	}
	if dpi <= 0 {
		return nil, fmt.Errorf("dpi must be positive, got %g", dpi)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Checked before parsing, because the parser's answer to a construct it
	// does not implement is to leave it out of the drawing without comment.
	if err := checkSupported(data); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	icon, err := oksvg.ReadIconStream(bytes.NewReader(data), oksvg.IgnoreErrorMode)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if icon.ViewBox.W <= 0 || icon.ViewBox.H <= 0 {
		return nil, fmt.Errorf("%s: no usable size: give the <svg> element a viewBox, or width and height", path)
	}

	w, h, err := pixelSize(icon.ViewBox.W, icon.ViewBox.H, targetWidthMM, targetHeightMM, dpi)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	// Zero-valued RGBA is transparent, so anything the artwork does not cover
	// stays transparent through to pdfgen — which is what makes -white auto
	// and -gloss artwork behave sensibly on an SVG with no background.
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	scanner := rasterx.NewScannerGV(w, h, img, img.Bounds())
	icon.SetTarget(0, 0, float64(w), float64(h))
	icon.Draw(rasterx.NewDasher(w, h, scanner), 1.0)
	return img, nil
}

// pixelSize picks a canvas that preserves the SVG's aspect ratio while
// clearing the requested DPI on both axes once scaled to the artboard.
func pixelSize(viewW, viewH, targetWidthMM, targetHeightMM, dpi float64) (w, h int, err error) {
	targetPxW := targetWidthMM / mmPerInch * dpi
	targetPxH := targetHeightMM / mmPerInch * dpi

	// Judged on the artboard alone, before the artwork is considered, so that
	// a resolution nothing could render is reported as exactly that rather
	// than being blamed on the shape of the artwork or quietly ignored by the
	// cap below.
	if targetPxW*targetPxH > maxPixels {
		return 0, 0, fmt.Errorf(
			"%g DPI on a %g × %g mm side needs %.0f × %.0f px, which is too large to render; lower the dpi",
			dpi, targetWidthMM, targetHeightMM, targetPxW, targetPxH)
	}

	svgAspect := viewW / viewH
	targetAspect := targetWidthMM / targetHeightMM

	if math.Abs(svgAspect-targetAspect)/targetAspect*100 > aspectSnapPct {
		// A genuinely different shape. Grow the canvas until both axes clear
		// the DPI floor while holding the SVG's own ratio, so the mismatch
		// survives to be reported rather than being absorbed here.
		pxW := math.Max(targetPxW, targetPxH*svgAspect)
		pxH := pxW / svgAspect

		// Holding a very different ratio while clearing the floor on both
		// axes can demand an enormous canvas: a 1000:30 banner on a portrait
		// face asks for 92000 px across. Artwork that far out of shape is
		// going to be refused for its shape, and that verdict does not need
		// the resolution, so cap the canvas rather than failing to allocate
		// one. Better to render something reportable than to answer a wrong
		// aspect ratio with an out-of-memory error.
		//
		// The cap can only take back what the mismatch added: the artboard's
		// own pixel count was checked against it above, so a canvas capped
		// here is still at least as large as the requested resolution asked
		// for on the axis that matters.
		if area := pxW * pxH; area > maxPixels {
			s := math.Sqrt(maxPixels / area)
			pxW, pxH = math.Floor(pxW*s), math.Floor(pxH*s)
		}
		return canvas(int(math.Ceil(pxW)), int(math.Ceil(pxH)), dpi)
	}

	// Artwork drawn for this side. Land on the artboard's proportions exactly
	// if a reasonable canvas can: rounding each axis up independently would
	// leave a fraction of a percent of aspect drift that artwork.Check then
	// reports back, and being warned about the quantization error in our own
	// arithmetic on every single file teaches the user to ignore the warning
	// that matters.
	if w, h, ok := exactRatio(targetWidthMM, targetHeightMM, targetPxW, targetPxH); ok {
		return canvas(w, h, dpi)
	}
	return canvas(int(math.Ceil(targetPxW)), int(math.Ceil(targetPxH)), dpi)
}

// exactRatio finds the smallest canvas whose pixel dimensions are in exactly
// the artboard's proportions and clear the requested resolution on both axes.
//
// The artboard table is written in whole and half millimetres, so reducing
// the two sides to lowest terms gives small numbers — 56 × 108.50 mm is 16:31
// — and a whole multiple of those is both an exact fit and a sensible size.
func exactRatio(widthMM, heightMM, minPxW, minPxH float64) (w, h int, ok bool) {
	// Hundredths of a millimetre: finer than any artboard is specified to,
	// and integral, which is what makes the ratio reducible.
	rw := int64(math.Round(widthMM * 100))
	rh := int64(math.Round(heightMM * 100))
	if rw <= 0 || rh <= 0 {
		return 0, 0, false
	}
	g := gcd(rw, rh)
	rw, rh = rw/g, rh/g

	n := math.Max(math.Ceil(minPxW/float64(rw)), math.Ceil(minPxH/float64(rh)))
	pxW, pxH := n*float64(rw), n*float64(rh)

	// An artboard whose ratio does not reduce to small terms would only be
	// met exactly by a canvas far bigger than was asked for. Nothing in the
	// table behaves that way, but an odd size passed in directly could, and
	// a slightly imperfect canvas beats quadrupling the memory for one.
	if pxW > 2*minPxW || pxW*pxH > maxPixels {
		return 0, 0, false
	}
	return int(pxW), int(pxH), true
}

func gcd(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// canvas rejects sizes that cannot be rendered.
func canvas(w, h int, dpi float64) (int, int, error) {
	if w < 1 || h < 1 {
		return 0, 0, fmt.Errorf("artboard and dpi give a canvas of %d × %d px", w, h)
	}
	if float64(w)*float64(h) > maxPixels {
		return 0, 0, fmt.Errorf("%d × %d px at %g DPI is too large to render; lower the dpi", w, h, dpi)
	}
	return w, h, nil
}

// unsupported lists the SVG constructs the rasterizer does not implement,
// mapped to what the user should do instead.
//
// Every one of these changes what ends up on the enclosure: text and images
// simply do not appear, and a clip, mask or filter that is not applied shows
// more than was drawn. Silence about that is the one outcome this tool must
// not produce, so hitting any of them is an error rather than a warning —
// there is no downstream check that would catch it, because a PDF of the
// wrong artwork is a perfectly valid PDF.
//
// Entries share a message where the advice is the same, and the messages are
// deduplicated, so a document full of <text> and <tspan> reports one line.
var unsupported = map[string]string{
	"text":     "text is not rendered: convert text to paths before exporting (in Inkscape, Path > Object to Path)",
	"tspan":    "text is not rendered: convert text to paths before exporting (in Inkscape, Path > Object to Path)",
	"textPath": "text is not rendered: convert text to paths before exporting (in Inkscape, Path > Object to Path)",
	"image":    "embedded raster images are not rendered: export the whole design as a PNG instead",
	"filter":   "filter effects are not rendered, so the artwork would print unfiltered: flatten them before exporting",
	"clipPath": "clipping is not applied, so clipped artwork would print in full: flatten it before exporting",
	"mask":     "masking is not applied, so masked artwork would print in full: flatten it before exporting",
	"pattern":  "pattern fills are not rendered: flatten them before exporting",
	"marker":   "markers are not rendered, so arrowheads and line ends would be missing: convert them to paths",
	"symbol":   "symbol definitions are not rendered: flatten them before exporting",
	"switch":   "conditional <switch> content is not evaluated: flatten it before exporting",
}

// unsupportedAttrs catches a clip, mask or filter applied by reference when
// the definition itself sits somewhere this scan would otherwise pass over.
var unsupportedAttrs = map[string]string{
	"clip-path": unsupported["clipPath"],
	"mask":      unsupported["mask"],
	"filter":    unsupported["filter"],
}

// checkSupported scans an SVG for constructs that would go missing.
func checkSupported(data []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	// Undeclared entities are a document problem, not something to guess at.
	dec.Strict = true

	seen := map[string]bool{}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("not readable as SVG: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if msg, bad := unsupported[se.Name.Local]; bad {
			seen[msg] = true
		}
		for _, a := range se.Attr {
			// "none" is how a document turns an inherited clip or filter back
			// off; it removes nothing.
			if msg, bad := unsupportedAttrs[a.Name.Local]; bad && strings.TrimSpace(a.Value) != "none" {
				seen[msg] = true
			}
		}
	}
	if len(seen) == 0 {
		return nil
	}

	msgs := make([]string, 0, len(seen))
	for m := range seen {
		msgs = append(msgs, m)
	}
	sort.Strings(msgs)

	var b strings.Builder
	b.WriteString("this SVG uses features the renderer does not support, and rendering it anyway would\n")
	b.WriteString("print artwork that differs from what you drew:\n")
	for _, m := range msgs {
		fmt.Fprintf(&b, "  - %s\n", m)
	}
	b.WriteString("export a PNG at the size 'tayda-uv sides' lists if flattening is awkward")
	return fmt.Errorf("%s", b.String())
}
