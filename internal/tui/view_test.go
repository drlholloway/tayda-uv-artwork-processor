package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/drlholloway/tayda-uv-artwork-processor/internal/enclosure"
	"github.com/drlholloway/tayda-uv-artwork-processor/internal/pdfgen"
)

// render returns a screen's text and logs it, so `go test -v ./internal/tui`
// shows the actual layout rather than only asserting about it.
func render(t *testing.T, m Model, label string) string {
	t.Helper()
	out := m.View()
	t.Logf("\n───── %s ─────\n%s", label, out)
	return out
}

func TestEnclosureScreenShowsEveryEnclosureWithItsFaceSize(t *testing.T) {
	m := newModel(t)
	out := render(t, m, "enclosure choice")

	for _, name := range enclosure.Names() {
		if !strings.Contains(out, name) {
			t.Errorf("screen omits %s", name)
		}
	}
	// The face size is what tells two similar enclosures apart at a glance.
	if !strings.Contains(out, "56 × 108.5 mm") {
		t.Error("screen should show each enclosure's face size")
	}
	if !strings.Contains(out, "▸") {
		t.Error("screen should mark the highlighted row")
	}
}

func TestSideTableShowsEverySideAndItsArtboard(t *testing.T) {
	dir := t.TempDir()
	m := pickEnclosure(t, newModel(t), "1590B")
	m = attach(m, enclosure.SideA, pickArtwork, writePNG(t, dir, "face.png", goodW, goodH, 255))
	m = attach(m, enclosure.SideC, pickArtwork, writePNG(t, dir, "side.png", 200, 860, 255))

	out := render(t, m, "side table")

	for _, side := range enclosure.Sides {
		if !strings.Contains(out, string(side)) {
			t.Errorf("table omits side %s", side)
		}
	}
	for _, want := range []string{
		"1590B",         // which enclosure
		"56 × 108.5 mm", // side A artboard
		"face.png",      // attached artwork
		"✓",             // side A is good
		"✗",             // side C is too low-res
		"· not set",     // untouched sides
		"white auto",    // current ink settings
		"gloss none",    //
		"c",             // the convert key is offered
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table omits %q", want)
		}
	}
}

// The table only has room for a symbol, so the full problem has to appear
// somewhere the user will read it before printing.
func TestSideTableExplainsTheHighlightedSideInFull(t *testing.T) {
	dir := t.TempDir()
	m := pickEnclosure(t, newModel(t), "1590B")
	m = attach(m, enclosure.SideA, pickArtwork, writePNG(t, dir, "small.png", 200, 400, 255))
	m.sideCursor = 0

	out := render(t, m, "side table with a problem highlighted")
	if !strings.Contains(out, "DPI minimum") {
		t.Error("the highlighted side's problem should be spelled out")
	}
	if !strings.Contains(out, "small.png") {
		t.Error("the detail area should name the file")
	}

	// A side with nothing on it should say how to fix that.
	m.sideCursor = 1
	out = render(t, m, "side table with an empty side highlighted")
	if !strings.Contains(out, "press enter") {
		t.Error("an empty side should tell the user what to do")
	}
}

func TestSideTableShowsPerSideGloss(t *testing.T) {
	dir := t.TempDir()
	art := writePNG(t, dir, "art.png", goodW, goodH, 255)
	m := pickEnclosure(t, newModel(t), "1590B")
	m = attach(m, enclosure.SideA, pickArtwork, art)
	m = attach(m, enclosure.SideA, pickMask, art)
	m = attach(m, enclosure.SideLid, pickArtwork, art)

	out := render(t, m, "side table with a per-side gloss mask")
	if !strings.Contains(out, "mask") {
		t.Error("a side with a gloss mask should say so in the table")
	}
}

func TestResultsScreenNamesEveryFileWritten(t *testing.T) {
	m := pickEnclosure(t, newModel(t), "1590B")
	m.screen = screenResults
	m.results = []result{
		{side: enclosure.SideA, path: "/tmp/1590B-A.pdf", layers: pdfgen.LayerSummary(pdfgen.WhiteAuto, pdfgen.GlossNone)},
		{side: enclosure.SideLid, path: "/tmp/1590B-Lid.pdf", layers: pdfgen.LayerSummary(pdfgen.WhiteAuto, pdfgen.GlossFull)},
	}
	out := render(t, m, "results")

	for _, want := range []string{"1590B-A.pdf", "1590B-Lid.pdf", "RDG_WHITE → CMYK", "RDG_GLOSS"} {
		if !strings.Contains(out, want) {
			t.Errorf("results screen omits %q", want)
		}
	}
	// The guide's advice matters more than the fact that files were written.
	if !strings.Contains(out, "PDF Analyzer") {
		t.Error("results should point at Tayda's PDF Analyzer")
	}
}

func TestResultsScreenReportsFailures(t *testing.T) {
	m := pickEnclosure(t, newModel(t), "1590B")
	m.screen = screenResults
	m.results = []result{{side: enclosure.SideA, path: "/tmp/1590B-A.pdf", err: errWrite}}

	out := render(t, m, "results with a failure")
	if !strings.Contains(out, "✗") || !strings.Contains(out, errWrite.Error()) {
		t.Error("a failed side should be shown with its error")
	}
	if strings.Contains(out, "PDF Analyzer") {
		t.Error("do not advise checking output that was not written")
	}
}

// Columns must line up regardless of how long a filename is.
func TestTableColumnsAreAligned(t *testing.T) {
	dir := t.TempDir()
	m := pickEnclosure(t, newModel(t), "1590B")
	m = attach(m, enclosure.SideA, pickArtwork,
		writePNG(t, dir, "a-very-long-artwork-filename-indeed.png", goodW, goodH, 255))

	out := render(t, m, "side table with a long filename")
	if !strings.Contains(out, "…") {
		t.Error("an over-long filename should be truncated rather than skew the table")
	}
}

func TestPadAndTruncate(t *testing.T) {
	if got := pad("ab", 5); got != "ab   " {
		t.Errorf("pad(ab,5) = %q", got)
	}
	if got := pad("abcdef", 3); got != "abcdef" {
		t.Errorf("pad should never shorten, got %q", got)
	}
	if got := truncate("abcdef", 4); got != "abc…" {
		t.Errorf("truncate(abcdef,4) = %q, want abc…", got)
	}
	if got := truncate("abc", 5); got != "abc" {
		t.Errorf("truncate should leave short strings alone, got %q", got)
	}
}

// errWrite stands in for a filesystem failure in results-screen tests.
var errWrite = errors.New("permission denied")
