// Package artwork loads source images and checks them against what the Tayda
// UV printing guide requires of the artwork placed on an enclosure side.
package artwork

import (
	"fmt"
	"image"
	"math"
	"os"

	// Registered for their side effects so Load accepts the formats a pedal
	// builder is likely to hand us.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/laneholloway/tayda-uv-artwork-processor/internal/enclosure"
)

// MinDPI is the resolution floor the guide states for embedded images
// ("make sure it's high resolution (300 DPI minimum)").
const MinDPI = 300.0

// maxAspectDriftPct is how far an image's aspect ratio may differ from the
// artboard's before we call it a problem. Scaling artwork to fill an artboard
// of a different shape stretches it; half a percent is around the point where
// that becomes visible on a pedal face.
const maxAspectDriftPct = 0.5

// Image is a decoded source image plus where it came from.
type Image struct {
	image.Image
	Path   string
	Format string
}

// Load decodes an image file.
func Load(path string) (*Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	img, format, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return &Image{Image: img, Path: path, Format: format}, nil
}

// Report is the outcome of checking one image against one enclosure side.
// Problems mean the file should not be sent to Tayda; Warnings are worth
// telling the user about but do not block the conversion.
type Report struct {
	Side     enclosure.Side
	Target   enclosure.Size
	PixelW   int
	PixelH   int
	DPIX     float64
	DPIY     float64
	Problems []string
	Warnings []string
}

// OK reports whether the image can be converted as-is.
func (r Report) OK() bool { return len(r.Problems) == 0 }

// Check measures an image against the artboard for a side.
//
// It deliberately does not check for a size in pixels: the artboard is
// specified in millimetres and any pixel count that clears the DPI floor at
// the right aspect ratio is fine.
func Check(img image.Image, side enclosure.Side, target enclosure.Size) Report {
	b := img.Bounds()
	r := Report{
		Side:   side,
		Target: target,
		PixelW: b.Dx(),
		PixelH: b.Dy(),
	}
	if r.PixelW == 0 || r.PixelH == 0 {
		r.Problems = append(r.Problems, "image has zero width or height")
		return r
	}

	// Effective DPI once the image is scaled to fill the artboard.
	r.DPIX = float64(r.PixelW) / (target.WidthMM / 25.4)
	r.DPIY = float64(r.PixelH) / (target.HeightMM / 25.4)

	if lowest := math.Min(r.DPIX, r.DPIY); lowest < MinDPI {
		r.Problems = append(r.Problems, fmt.Sprintf(
			"resolution is %.0f DPI at %s, below the %.0f DPI minimum: needs at least %d × %d px",
			lowest, target, MinDPI,
			pixelsFor(target.WidthMM, MinDPI), pixelsFor(target.HeightMM, MinDPI)))
	}

	imgAspect := float64(r.PixelW) / float64(r.PixelH)
	drift := math.Abs(imgAspect-target.AspectRatio()) / target.AspectRatio() * 100
	if drift > maxAspectDriftPct {
		r.Problems = append(r.Problems, fmt.Sprintf(
			"aspect ratio is %.4f but side %s is %.4f (%s), off by %.1f%%: the artwork would be stretched to fit",
			imgAspect, side, target.AspectRatio(), target, drift))
	} else if drift > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf(
			"aspect ratio is off by %.2f%%, within tolerance; artwork will be scaled to fill the artboard", drift))
	}

	if isPortraitMismatch(imgAspect, target.AspectRatio()) {
		r.Warnings = append(r.Warnings, fmt.Sprintf(
			"image is %s but side %s is %s: check the artwork is not rotated 90°",
			orientation(imgAspect), side, orientation(target.AspectRatio())))
	}
	return r
}

// RequiredPixels is the smallest pixel size that clears the DPI floor for a
// side, for telling the user what to re-export at.
func RequiredPixels(target enclosure.Size) (w, h int) {
	return pixelsFor(target.WidthMM, MinDPI), pixelsFor(target.HeightMM, MinDPI)
}

func pixelsFor(mm, dpi float64) int {
	return int(math.Ceil(mm / 25.4 * dpi))
}

func isPortraitMismatch(a, b float64) bool {
	return (a > 1 && b < 1) || (a < 1 && b > 1)
}

func orientation(aspect float64) string {
	switch {
	case aspect > 1:
		return "landscape"
	case aspect < 1:
		return "portrait"
	}
	return "square"
}
