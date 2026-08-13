package pdfgen

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
)

// objWriter assembles a PDF file: a header, a sequence of numbered indirect
// objects, then a cross-reference table and trailer.
//
// This is deliberately a small hand-rolled emitter rather than a dependency.
// The Tayda guide requires a specific and unusual combination — DeviceCMYK
// image data, Separation colour spaces with exact spot names, and optional
// content groups named CMYK / RDG_WHITE / RDG_GLOSS — and writing the objects
// directly is the most direct way to guarantee the output matches it.
type objWriter struct {
	buf     bytes.Buffer
	offsets map[int]int
	next    int
	err     error
}

func newObjWriter() *objWriter {
	w := &objWriter{offsets: map[int]int{}, next: 1}
	w.buf.WriteString("%PDF-1.7\n")
	// A comment line of bytes >127 marks the file as binary so that naive
	// tooling does not mangle it in transit.
	w.buf.Write([]byte{'%', 0xE2, 0xE3, 0xCF, 0xD3, '\n'})
	return w
}

// reserve allocates an object number without writing its body, so objects that
// reference one another can be emitted in any order.
func (w *objWriter) reserve() int {
	n := w.next
	w.next++
	return n
}

// put writes a plain (non-stream) object body.
func (w *objWriter) put(num int, body string) {
	w.offsets[num] = w.buf.Len()
	fmt.Fprintf(&w.buf, "%d 0 obj\n%s\nendobj\n", num, body)
}

// putStream writes a stream object, deflating data and supplying the /Filter
// and /Length entries itself. dictEntries is the rest of the stream
// dictionary, without the enclosing double angle brackets.
func (w *objWriter) putStream(num int, dictEntries string, data []byte) {
	if w.err != nil {
		return
	}
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	if _, err := zw.Write(data); err != nil {
		w.err = err
		return
	}
	if err := zw.Close(); err != nil {
		w.err = err
		return
	}
	w.offsets[num] = w.buf.Len()
	fmt.Fprintf(&w.buf, "%d 0 obj\n<<%s/Filter/FlateDecode/Length %d>>\nstream\n", num, dictEntries, z.Len())
	w.buf.Write(z.Bytes())
	w.buf.WriteString("\nendstream\nendobj\n")
}

// finish appends the cross-reference table and trailer, then writes the file.
func (w *objWriter) finish(root int, out io.Writer) error {
	if w.err != nil {
		return w.err
	}
	// Object 0 is always the head of the free list, so the table covers
	// 0..next-1.
	count := w.next
	for i := 1; i < count; i++ {
		if _, ok := w.offsets[i]; !ok {
			return fmt.Errorf("internal error: object %d was reserved but never written", i)
		}
	}

	start := w.buf.Len()
	fmt.Fprintf(&w.buf, "xref\n0 %d\n", count)
	w.buf.WriteString("0000000000 65535 f \n")
	for i := 1; i < count; i++ {
		// Each entry is exactly 20 bytes, trailing space included.
		fmt.Fprintf(&w.buf, "%010d 00000 n \n", w.offsets[i])
	}
	fmt.Fprintf(&w.buf, "trailer\n<</Size %d/Root %d 0 R>>\nstartxref\n%d\n%%%%EOF\n", count, root, start)

	_, err := out.Write(w.buf.Bytes())
	return err
}
