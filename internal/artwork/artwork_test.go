package artwork

import (
	"image"
	"strings"
	"testing"

	"github.com/drlholloway/tayda-uv-artwork-processor/internal/enclosure"
)

// 1590B side A: 56 × 108.50 mm. At 300 DPI that is 662 × 1282 px.
var side1590BA = enclosure.Size{WidthMM: 56, HeightMM: 108.50}

func checkPx(t *testing.T, w, h int, target enclosure.Size) Report {
	t.Helper()
	return Check(image.NewRGBA(image.Rect(0, 0, w, h)), enclosure.SideA, target)
}

func TestExactlyAtMinimumDPIPasses(t *testing.T) {
	w, h := RequiredPixels(side1590BA)
	r := checkPx(t, w, h, side1590BA)
	if !r.OK() {
		t.Errorf("%d × %d px should pass at %v: %v", w, h, side1590BA, r.Problems)
	}
	if r.DPIX < MinDPI || r.DPIY < MinDPI {
		t.Errorf("DPI %.1f × %.1f should both be >= %.0f", r.DPIX, r.DPIY, MinDPI)
	}
}

func TestLowResolutionIsAProblem(t *testing.T) {
	// Right shape, half the resolution.
	r := checkPx(t, 331, 641, side1590BA)
	if r.OK() {
		t.Fatal("half-resolution image should be rejected")
	}
	if !strings.Contains(r.Problems[0], "DPI") {
		t.Errorf("problem should mention DPI, got %q", r.Problems[0])
	}
	// The message must tell the user what to re-export at.
	if !strings.Contains(r.Problems[0], "662") {
		t.Errorf("problem should state the required pixel size, got %q", r.Problems[0])
	}
}

func TestWrongAspectRatioIsAProblem(t *testing.T) {
	// Enough pixels, but square rather than tall.
	r := checkPx(t, 1300, 1300, side1590BA)
	if r.OK() {
		t.Fatal("square image on a tall artboard should be rejected")
	}
	joined := strings.Join(r.Problems, " ")
	if !strings.Contains(joined, "aspect ratio") {
		t.Errorf("problem should mention aspect ratio, got %q", joined)
	}
}

func TestRotatedArtworkIsRejected(t *testing.T) {
	// Side A dimensions transposed: a common mistake worth catching.
	w, h := RequiredPixels(side1590BA)
	r := checkPx(t, h, w, side1590BA)
	if r.OK() {
		t.Fatal("90°-rotated artwork should be rejected")
	}
}

func TestTinyAspectDriftIsOnlyAWarning(t *testing.T) {
	// 662 × 1282 is the exact target; 662 × 1284 drifts ~0.16%.
	r := checkPx(t, 662, 1284, side1590BA)
	if !r.OK() {
		t.Errorf("sub-tolerance drift should pass, got %v", r.Problems)
	}
	if len(r.Warnings) == 0 {
		t.Error("sub-tolerance drift should warn")
	}
}

// A gloss mask decides where varnish lands, so its shape matters as much as
// the artwork's — but a slightly soft varnish edge is not worth blocking a
// conversion over.
func TestMaskResolutionIsAdvisoryButShapeIsNot(t *testing.T) {
	lowRes := image.NewRGBA(image.Rect(0, 0, 331, 641)) // right shape, half DPI
	r := CheckMask(lowRes, enclosure.SideA, side1590BA)
	if !r.OK() {
		t.Errorf("low-resolution mask should not block: %v", r.Problems)
	}
	if len(r.Warnings) == 0 {
		t.Error("low-resolution mask should warn")
	}

	wrongShape := image.NewRGBA(image.Rect(0, 0, 1300, 1300))
	if CheckMask(wrongShape, enclosure.SideA, side1590BA).OK() {
		t.Error("a mask of the wrong shape puts varnish in the wrong place and should be rejected")
	}

	// Artwork itself keeps the hard resolution floor.
	if Check(lowRes, enclosure.SideA, side1590BA).OK() {
		t.Error("low-resolution artwork should still be rejected")
	}
}

func TestZeroSizedImage(t *testing.T) {
	r := checkPx(t, 0, 0, side1590BA)
	if r.OK() {
		t.Error("zero-sized image should be rejected")
	}
}

// Small sides have small pixel requirements; make sure nothing assumes a
// face-sized artboard.
func TestSmallSideRequirements(t *testing.T) {
	side := enclosure.Size{WidthMM: 30, HeightMM: 25} // 1590A side B
	w, h := RequiredPixels(side)
	if w != 355 || h != 296 {
		t.Errorf("RequiredPixels(%v) = %d × %d, want 355 × 296", side, w, h)
	}
	if r := Check(image.NewRGBA(image.Rect(0, 0, w, h)), enclosure.SideB, side); !r.OK() {
		t.Errorf("minimum-size image should pass: %v", r.Problems)
	}
}
