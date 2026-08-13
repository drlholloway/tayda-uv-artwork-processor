package pdfinspect

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"strings"
	"testing"
)

// pdf assembles a file from object bodies. The reader scans for object headers
// rather than following the cross-reference table, so these fixtures can leave
// out the xref and still be read exactly as a real file would be.
func pdf(objs ...string) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n")
	for i, body := range objs {
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}
	b.WriteString("trailer\n<</Size 1/Root 1 0 R>>\n%%EOF\n")
	return b.Bytes()
}

func deflate(t *testing.T, data []byte) []byte {
	t.Helper()
	var z bytes.Buffer
	w := zlib.NewWriter(&z)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return z.Bytes()
}

// streamObj builds a deflated stream object body.
func streamObj(t *testing.T, dictEntries string, data []byte) string {
	t.Helper()
	z := deflate(t, data)
	return fmt.Sprintf("<<%s/Filter/FlateDecode/Length %d>>\nstream\n%s\nendstream",
		dictEntries, len(z), z)
}

// A page of exactly one enclosure side, with nothing else in it.
func minimalPDF(t *testing.T, mediaBox string) []byte {
	t.Helper()
	return pdf(
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		"<</Type/Page/Parent 2 0 R/MediaBox"+mediaBox+"/Contents 4 0 R>>",
		streamObj(t, "", []byte("q Q\n")),
	)
}

func TestReadsArtboardInMillimetres(t *testing.T) {
	// 158.74 × 307.56 pt is 1590B side A.
	r, err := Inspect(minimalPDF(t, "[0 0 158.740157 307.559055]"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Pages != 1 {
		t.Errorf("pages = %d, want 1", r.Pages)
	}
	if d := r.WidthMM - 56; d > 0.001 || d < -0.001 {
		t.Errorf("width = %v mm, want 56", r.WidthMM)
	}
	if d := r.HeightMM - 108.5; d > 0.001 || d < -0.001 {
		t.Errorf("height = %v mm, want 108.5", r.HeightMM)
	}
}

// A MediaBox need not start at the origin; the size is the difference.
func TestMediaBoxAwayFromTheOrigin(t *testing.T) {
	r, err := Inspect(minimalPDF(t, "[10 20 82 92]"))
	if err != nil {
		t.Fatal(err)
	}
	if r.WidthPt != 72 || r.HeightPt != 72 {
		t.Errorf("got %v × %v pt, want 72 × 72", r.WidthPt, r.HeightPt)
	}
}

// MediaBox set on a Pages node applies to the pages beneath it.
func TestMediaBoxIsInheritedFromThePageTree(t *testing.T) {
	b := pdf(
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1/MediaBox[0 0 144 288]>>",
		"<</Type/Page/Parent 2 0 R>>",
	)
	r, err := Inspect(b)
	if err != nil {
		t.Fatal(err)
	}
	if r.WidthPt != 144 || r.HeightPt != 288 {
		t.Errorf("got %v × %v pt, want 144 × 288 inherited from the Pages node", r.WidthPt, r.HeightPt)
	}
}

func TestRejectsSomethingThatIsNotAPDF(t *testing.T) {
	if _, err := Inspect([]byte("\x89PNG\r\n\x1a\n....")); err == nil {
		t.Fatal("a PNG was accepted as a PDF")
	}
}

// An encrypted file must be reported as unreadable rather than as clean: none
// of its strings or streams mean what they appear to.
func TestRejectsAnEncryptedFile(t *testing.T) {
	b := pdf(
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 72 72]>>",
		"<</Filter/Standard/V 2/R 3/O<00>/U<00>/P -1>>",
	)
	_, err := Inspect(b)
	if err == nil || !strings.Contains(err.Error(), "encrypted") {
		t.Fatalf("err = %v, want an encryption complaint", err)
	}
}

// A file with no page tree must fail rather than report a zero-sized artboard,
// which would read as "0 × 0 mm, no problems found".
func TestFailsRatherThanReportingAnEmptyDocument(t *testing.T) {
	b := pdf("<</Type/Catalog>>", "<</Something/Else>>")
	if _, err := Inspect(b); err == nil {
		t.Fatal("a document with no pages was accepted")
	}
}

// Stream payloads are arbitrary bytes. Something inside one that looks like an
// object header must not shadow the real object of that number.
func TestStreamBytesCannotInventAnObject(t *testing.T) {
	// Object 3 is the real page. The content stream contains text that would
	// parse as a competing definition of object 3 with a different size.
	trap := []byte("3 0 obj\n<</Type/Page/MediaBox[0 0 1 1]>>\nendobj\n")
	b := pdf(
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 144 288]/Contents 4 0 R>>",
		// Stored uncompressed so the trap text really does sit in the file.
		fmt.Sprintf("<</Length %d>>\nstream\n%s\nendstream", len(trap), trap),
	)
	r, err := Inspect(b)
	if err != nil {
		t.Fatal(err)
	}
	if r.WidthPt != 144 || r.HeightPt != 288 {
		t.Errorf("got %v × %v pt, want 144 × 288: an object header inside a stream was honoured",
			r.WidthPt, r.HeightPt)
	}
}

// PDF 1.5 writers pack most objects into object streams. Without unpacking
// them the file looks empty, which would otherwise be reported as clean.
func TestObjectStreamsAreUnpacked(t *testing.T) {
	bodies := []string{
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 144 288]>>",
	}
	// The stream starts with N pairs of "objectNumber byteOffset", where each
	// offset is measured from /First.
	var head, packed strings.Builder
	for i, body := range bodies {
		fmt.Fprintf(&head, "%d %d ", i+1, packed.Len())
		packed.WriteString(body)
		packed.WriteString(" ")
	}
	first := head.Len()
	data := []byte(head.String() + packed.String())

	b := pdf(streamObj(t, fmt.Sprintf("/Type/ObjStm/N %d/First %d", len(bodies), first), data))
	r, err := Inspect(b)
	if err != nil {
		t.Fatal(err)
	}
	if r.WidthPt != 144 || r.HeightPt != 288 {
		t.Errorf("got %v × %v pt, want 144 × 288 from inside the object stream", r.WidthPt, r.HeightPt)
	}
}

// The guide is explicit that the spot name is case-sensitive, so the reader
// must report the name exactly as written rather than normalising it.
func TestSpotColourNamesAreReportedVerbatim(t *testing.T) {
	b := pdf(
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 72 72]/Resources<</ColorSpace<</Cs0 4 0 R>>>>>>",
		"[/Separation/Rdg_White/DeviceCMYK<</FunctionType 2/Domain[0 1]/C0[0 0 0 0]/C1[0 0 0 1]/N 1>>]",
	)
	r, err := Inspect(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.SpotColors) != 1 || r.SpotColors[0] != "Rdg_White" {
		t.Errorf("spots = %v, want [Rdg_White] exactly as written", r.SpotColors)
	}
}

// A name may be written with #xx escapes, so RDG#5FWHITE is RDG_WHITE. A
// reader that missed this would report a spot name that does not exist.
func TestHexEscapesInNamesAreDecoded(t *testing.T) {
	b := pdf(
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 72 72]/Resources<</ColorSpace<</Cs0 4 0 R>>>>>>",
		"[/Separation/RDG#5FWHITE/DeviceCMYK<</FunctionType 2/N 1>>]",
	)
	r, err := Inspect(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.SpotColors) != 1 || r.SpotColors[0] != "RDG_WHITE" {
		t.Errorf("spots = %v, want [RDG_WHITE] with the #5F decoded", r.SpotColors)
	}
}

// /All and /None are reserved colorant names, not inks anyone ordered.
func TestReservedColorantNamesAreNotSpotInks(t *testing.T) {
	b := pdf(
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 72 72]/Resources<</ColorSpace<</Cs0 4 0 R>>>>>>",
		"[/Separation/All/DeviceCMYK<</FunctionType 2/N 1>>]",
	)
	r, err := Inspect(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.SpotColors) != 0 {
		t.Errorf("spots = %v, want none: /All is reserved", r.SpotColors)
	}
}

// RGB hidden inside a compressed content stream still has to be found — that
// is exactly where it hides, since the dictionaries look clean.
func TestRGBIsFoundInsideACompressedStream(t *testing.T) {
	b := pdf(
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 72 72]/Contents 4 0 R>>",
		streamObj(t, "", []byte("/DeviceRGB cs 1 0 0 sc 0 0 72 72 re f\n")),
	)
	r, err := Inspect(b)
	if err != nil {
		t.Fatal(err)
	}
	if !r.HasRGB() {
		t.Errorf("colour spaces = %v, want DeviceRGB found inside the deflated stream", r.ColorSpaces)
	}
}

// The paint order is the point of the whole check, so it is read from the
// content stream and mapped back through the page's /Properties.
func TestPaintOrderIsReadThroughTheResourceNames(t *testing.T) {
	content := []byte("/OC /P0 BDC\nq Q\nEMC\n/OC /P1 BDC\nq Q\nEMC\n/OC /P2 BDC\nq Q\nEMC\n")
	b := pdf(
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 72 72]/Contents 4 0 R"+
			"/Resources<</Properties<</P0 5 0 R/P1 6 0 R/P2 7 0 R>>>>>>",
		streamObj(t, "", content),
		"<</Type/OCG/Name(RDG_WHITE)>>",
		"<</Type/OCG/Name(CMYK)>>",
		"<</Type/OCG/Name(RDG_GLOSS)>>",
	)
	r, err := Inspect(b)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"RDG_WHITE", "CMYK", "RDG_GLOSS"}
	if strings.Join(r.PaintOrder, ",") != strings.Join(want, ",") {
		t.Errorf("paint order = %v, want %v", r.PaintOrder, want)
	}
	// The resource names are an implementation detail of the writer; the layer
	// list is what a printer sees.
	if len(r.Layers) != 3 {
		t.Errorf("layers = %v, want three", r.Layers)
	}
}

// The order in the file is what counts, not the order the layers are declared
// in, so a file that paints white last must read back that way.
func TestPaintOrderReflectsTheFileNotTheDeclaration(t *testing.T) {
	content := []byte("/OC /P1 BDC\nq Q\nEMC\n/OC /P0 BDC\nq Q\nEMC\n")
	b := pdf(
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 72 72]/Contents 4 0 R"+
			"/Resources<</Properties<</P0 5 0 R/P1 6 0 R>>>>>>",
		streamObj(t, "", content),
		"<</Type/OCG/Name(RDG_WHITE)>>",
		"<</Type/OCG/Name(CMYK)>>",
	)
	r, err := Inspect(b)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"CMYK", "RDG_WHITE"}
	if strings.Join(r.PaintOrder, ",") != strings.Join(want, ",") {
		t.Errorf("paint order = %v, want %v", r.PaintOrder, want)
	}
}

// A literal string may carry escapes; a layer name must survive them.
func TestLayerNameEscapesAreDecoded(t *testing.T) {
	b := pdf(
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 72 72]>>",
		`<</Type/OCG/Name(RDG\137WHITE)>>`, // \137 is octal for underscore
	)
	r, err := Inspect(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Layers) != 1 || r.Layers[0] != "RDG_WHITE" {
		t.Errorf("layers = %v, want [RDG_WHITE]", r.Layers)
	}
}

// Multi-page files are a mistake for this service, and the count has to be
// right for the command to say so.
func TestCountsEveryPageInTheTree(t *testing.T) {
	b := pdf(
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R 4 0 R]/Count 2/MediaBox[0 0 72 72]>>",
		"<</Type/Page/Parent 2 0 R>>",
		"<</Type/Page/Parent 2 0 R>>",
	)
	r, err := Inspect(b)
	if err != nil {
		t.Fatal(err)
	}
	if r.Pages != 2 {
		t.Errorf("pages = %d, want 2", r.Pages)
	}
}
