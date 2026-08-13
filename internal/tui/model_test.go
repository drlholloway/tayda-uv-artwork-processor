package tui

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drlholloway/tayda-uv-artwork-processor/internal/enclosure"
	"github.com/drlholloway/tayda-uv-artwork-processor/internal/pdfgen"
)

// 1590B side A is 56 × 108.5 mm. That ratio reduces to 112:217, so 672 × 1302
// is both exactly the right shape and comfortably over 300 DPI — artwork that
// validates with no warnings at all.
const (
	goodW, goodH = 672, 1302
)

func writePNG(t *testing.T, dir, name string, w, h int, alpha uint8) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{R: 200, G: 40, B: 60, A: alpha})
		}
	}
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return path
}

func newModel(t *testing.T) Model {
	t.Helper()
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	m.outDir = t.TempDir()
	return m
}

// press sends a keystroke and returns the resulting model.
func press(t *testing.T, m Model, k string) Model {
	t.Helper()
	next, _ := m.Update(keyMsg(k))
	return next.(Model)
}

func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
}

// attach puts a file on a side the way the picker would.
func attach(m Model, side enclosure.Side, what picking, path string) Model {
	m.pickFor, m.pickWhat = side, what
	(&m).attach(path)
	return m
}

// pickEnclosure walks the first screen to the named enclosure.
func pickEnclosure(t *testing.T, m Model, name string) Model {
	t.Helper()
	for i, n := range m.names {
		if n == name {
			m.encCursor = i
			return press(t, m, "enter")
		}
	}
	t.Fatalf("enclosure %q not offered", name)
	return m
}

func TestStartsOnEnclosureChoice(t *testing.T) {
	m := newModel(t)
	if m.screen != screenEnclosure {
		t.Errorf("screen = %v, want the enclosure list", m.screen)
	}
	if len(m.names) != len(enclosure.Names()) {
		t.Error("the enclosure list should offer every known enclosure")
	}
	if m.white != pdfgen.WhiteAuto || m.gloss != pdfgen.GlossNone {
		t.Error("defaults should match the CLI: white auto, gloss none")
	}
}

func TestChoosingAnEnclosureOpensTheSideTable(t *testing.T) {
	m := pickEnclosure(t, newModel(t), "1590B")
	if m.screen != screenSides {
		t.Fatalf("screen = %v, want the side table", m.screen)
	}
	if m.enc.Name != "1590B" {
		t.Errorf("enclosure = %s, want 1590B", m.enc.Name)
	}
}

// Every side is a different size on a different enclosure, so artwork chosen
// for one cannot carry over to another.
func TestChangingEnclosureDiscardsArtwork(t *testing.T) {
	dir := t.TempDir()
	art := writePNG(t, dir, "face.png", goodW, goodH, 255)

	m := pickEnclosure(t, newModel(t), "1590B")
	m = attach(m, enclosure.SideA, pickArtwork, art)
	if m.sides[enclosure.SideA] == nil {
		t.Fatal("artwork was not attached")
	}

	m = press(t, m, "e") // back to the enclosure list
	m = pickEnclosure(t, m, "1590A")
	if len(m.sides) != 0 {
		t.Error("artwork from the previous enclosure should have been discarded")
	}

	// Re-picking the same enclosure must not throw work away.
	m = pickEnclosure(t, newModel(t), "1590B")
	m = attach(m, enclosure.SideA, pickArtwork, art)
	m = press(t, m, "e")
	m = pickEnclosure(t, m, "1590B")
	if len(m.sides) != 1 {
		t.Error("re-selecting the same enclosure should keep artwork")
	}
}

func TestCyclingInkSettings(t *testing.T) {
	m := pickEnclosure(t, newModel(t), "1590B")

	// white: auto -> full -> none -> auto
	for _, want := range []pdfgen.WhiteMode{pdfgen.WhiteFull, pdfgen.WhiteNone, pdfgen.WhiteAuto} {
		m = press(t, m, "w")
		if m.white != want {
			t.Errorf("white = %v, want %v", m.white, want)
		}
	}
	// gloss: none -> full -> artwork -> none. Mask is per-side, never global.
	for _, want := range []pdfgen.GlossMode{pdfgen.GlossFull, pdfgen.GlossArtwork, pdfgen.GlossNone} {
		m = press(t, m, "g")
		if m.gloss != want {
			t.Errorf("gloss = %v, want %v", m.gloss, want)
		}
		if m.gloss == pdfgen.GlossMask {
			t.Fatal("cycling should never land on mask mode")
		}
	}
}

func TestAttachingArtworkValidatesIt(t *testing.T) {
	dir := t.TempDir()
	good := writePNG(t, dir, "good.png", goodW, goodH, 255)
	small := writePNG(t, dir, "small.png", goodW/2, goodH/2, 255)

	m := pickEnclosure(t, newModel(t), "1590B")

	m = attach(m, enclosure.SideA, pickArtwork, good)
	if st := m.sides[enclosure.SideA]; st == nil || !st.artRep.OK() {
		t.Error("a correctly sized image should validate clean")
	}

	m = attach(m, enclosure.SideB, pickArtwork, small)
	if st := m.sides[enclosure.SideB]; st == nil || st.artRep.OK() {
		t.Error("a half-resolution image should be flagged")
	}
}

func TestClearingASide(t *testing.T) {
	dir := t.TempDir()
	m := pickEnclosure(t, newModel(t), "1590B")
	m = attach(m, enclosure.SideA, pickArtwork, writePNG(t, dir, "a.png", goodW, goodH, 255))

	m.sideCursor = 0 // side A
	m = press(t, m, "x")
	if m.sides[enclosure.SideA] != nil {
		t.Error("x should clear the highlighted side")
	}
}

// A gloss mask on one side must not turn gloss on everywhere else.
func TestGlossMaskIsPerSide(t *testing.T) {
	dir := t.TempDir()
	art := writePNG(t, dir, "art.png", goodW, goodH, 255)
	mask := writePNG(t, dir, "mask.png", goodW, goodH, 255)

	m := pickEnclosure(t, newModel(t), "1590B")
	m = attach(m, enclosure.SideA, pickArtwork, art)
	m = attach(m, enclosure.SideA, pickMask, mask)
	m = attach(m, enclosure.SideLid, pickArtwork, art)

	if got := m.sides[enclosure.SideA].gloss(m.gloss); got != pdfgen.GlossMask {
		t.Errorf("side with a mask: gloss = %v, want mask", got)
	}
	if got := m.sides[enclosure.SideLid].gloss(m.gloss); got != pdfgen.GlossNone {
		t.Errorf("side without a mask: gloss = %v, want none", got)
	}
}

func TestMaskNeedsArtworkFirst(t *testing.T) {
	m := pickEnclosure(t, newModel(t), "1590B")
	m.sideCursor = 0
	m = press(t, m, "m")
	if m.screen == screenPicker {
		t.Error("should not open a mask picker for a side with no artwork")
	}
	if m.status == "" {
		t.Error("should explain why nothing happened")
	}
}

func TestConvertRefusesWhenThereIsNothingToDo(t *testing.T) {
	m := pickEnclosure(t, newModel(t), "1590B")
	next, cmd := m.Update(keyMsg("c"))
	if cmd != nil {
		t.Error("convert should not run with no artwork attached")
	}
	if next.(Model).status == "" {
		t.Error("should say why nothing happened")
	}
}

// The TUI has no -force. Refusing here matches the CLI's default, and the
// override stays a deliberate command-line act.
func TestConvertRefusesArtworkThatWillNotPrint(t *testing.T) {
	dir := t.TempDir()
	m := pickEnclosure(t, newModel(t), "1590B")
	m = attach(m, enclosure.SideA, pickArtwork, writePNG(t, dir, "small.png", 100, 200, 255))

	next, cmd := m.Update(keyMsg("c"))
	if cmd != nil {
		t.Fatal("convert should refuse artwork with problems")
	}
	if !strings.Contains(next.(Model).status, "side A") {
		t.Errorf("status should name the offending side, got %q", next.(Model).status)
	}
}

func TestConvertWritesEveryAttachedSide(t *testing.T) {
	dir := t.TempDir()
	art := writePNG(t, dir, "face.png", goodW, goodH, 255)

	m := pickEnclosure(t, newModel(t), "1590B")
	m = attach(m, enclosure.SideA, pickArtwork, art)
	m = attach(m, enclosure.SideLid, pickArtwork, art)

	_, cmd := m.Update(keyMsg("c"))
	if cmd == nil {
		t.Fatal("convert should have run")
	}
	msg := cmd()
	done, ok := msg.(convertedMsg)
	if !ok {
		t.Fatalf("expected a convertedMsg, got %T", msg)
	}
	if len(done.results) != 2 {
		t.Fatalf("wrote %d files, want 2", len(done.results))
	}

	next, _ := m.Update(done)
	m = next.(Model)
	if m.screen != screenResults {
		t.Error("should land on the results screen")
	}

	for _, r := range done.results {
		if r.err != nil {
			t.Errorf("%s: %v", r.path, r.err)
			continue
		}
		info, err := os.Stat(r.path)
		if err != nil {
			t.Errorf("%s was not written: %v", r.path, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", r.path)
		}
		if !strings.HasPrefix(filepath.Base(r.path), "1590B-") {
			t.Errorf("unexpected output name %s", filepath.Base(r.path))
		}
	}
}

// Sides with no artwork are skipped rather than written blank.
func TestConvertSkipsEmptySides(t *testing.T) {
	dir := t.TempDir()
	m := pickEnclosure(t, newModel(t), "1590B")
	m = attach(m, enclosure.SideA, pickArtwork, writePNG(t, dir, "face.png", goodW, goodH, 255))

	_, cmd := m.Update(keyMsg("c"))
	done := cmd().(convertedMsg)
	if len(done.results) != 1 {
		t.Fatalf("wrote %d files, want only the one side with artwork", len(done.results))
	}
	if done.results[0].side != enclosure.SideA {
		t.Errorf("wrote side %s, want A", done.results[0].side)
	}
}

func TestCtrlCQuitsFromEveryScreen(t *testing.T) {
	m := newModel(t)
	for _, s := range []screen{screenEnclosure, screenSides, screenPicker, screenResults} {
		m.screen = s
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		if cmd == nil {
			t.Errorf("screen %v: ctrl+c did not quit", s)
		}
	}
}
