// Package pdfinspect reads a finished PDF back and reports the facts the Tayda
// guide cares about: the artboard size, the colour spaces used, the spot ink
// names, and the order the layers are painted in.
//
// It exists because this repo writes PDFs by hand. A hand-rolled writer needs
// a reader that does not share its assumptions — one that parses the file the
// way an unfamiliar consumer would, so that a mistake in the writer shows up
// as a wrong reading rather than being reproduced on both sides.
//
// It is deliberately a partial PDF implementation. It parses enough to answer
// those questions and returns an error for anything it does not understand.
// Reporting "unreadable" is correct; reporting "clean" about a file it failed
// to parse would be worse than having no checker at all.
package pdfinspect

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"regexp"
	"strconv"
)

// The PDF object model, as much of it as is needed here.
type (
	name  string
	ref   struct{ num, gen int }
	dict  map[name]any
	array []any
	str   []byte
)

// stream is a dictionary with data attached. The data is kept raw; decode
// applies the filters on demand.
type stream struct {
	d   dict
	raw []byte
}

// lexer walks the bytes of one object.
type lexer struct {
	b []byte
	i int
}

func (l *lexer) skipSpace() {
	for l.i < len(l.b) {
		c := l.b[l.i]
		switch {
		case c == '%': // comment to end of line
			for l.i < len(l.b) && l.b[l.i] != '\n' && l.b[l.i] != '\r' {
				l.i++
			}
		case c == 0 || c == '\t' || c == '\n' || c == '\f' || c == '\r' || c == ' ':
			l.i++
		default:
			return
		}
	}
}

func isDelim(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

func isRegular(c byte) bool {
	if isDelim(c) {
		return false
	}
	switch c {
	case 0, '\t', '\n', '\f', '\r', ' ':
		return false
	}
	return true
}

// parseObject reads one object at the current position.
func (l *lexer) parseObject() (any, error) {
	l.skipSpace()
	if l.i >= len(l.b) {
		return nil, io.ErrUnexpectedEOF
	}
	switch c := l.b[l.i]; {
	case c == '/':
		return l.parseName()
	case c == '(':
		return l.parseLiteralString()
	case c == '[':
		return l.parseArray()
	case c == '<':
		if l.i+1 < len(l.b) && l.b[l.i+1] == '<' {
			return l.parseDict()
		}
		return l.parseHexString()
	case c == ']' || c == '>' || c == '}':
		return nil, fmt.Errorf("unexpected %q", c)
	default:
		return l.parseKeywordOrNumber()
	}
}

func (l *lexer) parseName() (name, error) {
	l.i++ // past '/'
	var out []byte
	for l.i < len(l.b) && isRegular(l.b[l.i]) {
		c := l.b[l.i]
		// #xx is a hex escape, which is how a name holding a space or a
		// delimiter is written.
		if c == '#' && l.i+2 < len(l.b) {
			if v, err := strconv.ParseUint(string(l.b[l.i+1:l.i+3]), 16, 8); err == nil {
				out = append(out, byte(v))
				l.i += 3
				continue
			}
		}
		out = append(out, c)
		l.i++
	}
	return name(out), nil
}

func (l *lexer) parseLiteralString() (str, error) {
	l.i++ // past '('
	var out []byte
	depth := 1
	for l.i < len(l.b) {
		c := l.b[l.i]
		l.i++
		switch c {
		case '\\':
			if l.i >= len(l.b) {
				return nil, io.ErrUnexpectedEOF
			}
			e := l.b[l.i]
			l.i++
			switch e {
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case '\n':
				// line continuation, contributes nothing
			case '\r':
				if l.i < len(l.b) && l.b[l.i] == '\n' {
					l.i++
				}
			default:
				if e >= '0' && e <= '7' { // octal, up to three digits
					v := int(e - '0')
					for n := 0; n < 2 && l.i < len(l.b) && l.b[l.i] >= '0' && l.b[l.i] <= '7'; n++ {
						v = v*8 + int(l.b[l.i]-'0')
						l.i++
					}
					out = append(out, byte(v))
				} else {
					out = append(out, e)
				}
			}
		case '(':
			depth++
			out = append(out, c)
		case ')':
			depth--
			if depth == 0 {
				return str(out), nil
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return nil, io.ErrUnexpectedEOF
}

func (l *lexer) parseHexString() (str, error) {
	l.i++ // past '<'
	var digits []byte
	for l.i < len(l.b) && l.b[l.i] != '>' {
		c := l.b[l.i]
		if !(c == 0 || c == '\t' || c == '\n' || c == '\f' || c == '\r' || c == ' ') {
			digits = append(digits, c)
		}
		l.i++
	}
	if l.i >= len(l.b) {
		return nil, io.ErrUnexpectedEOF
	}
	l.i++ // past '>'
	if len(digits)%2 == 1 {
		digits = append(digits, '0') // an odd final digit is padded with zero
	}
	out := make([]byte, len(digits)/2)
	for i := range out {
		v, err := strconv.ParseUint(string(digits[i*2:i*2+2]), 16, 8)
		if err != nil {
			return nil, fmt.Errorf("bad hex string: %w", err)
		}
		out[i] = byte(v)
	}
	return str(out), nil
}

func (l *lexer) parseArray() (array, error) {
	l.i++ // past '['
	out := array{}
	for {
		l.skipSpace()
		if l.i >= len(l.b) {
			return nil, io.ErrUnexpectedEOF
		}
		if l.b[l.i] == ']' {
			l.i++
			return out, nil
		}
		v, err := l.parseObject()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
}

func (l *lexer) parseDict() (any, error) {
	l.i += 2 // past '<<'
	d := dict{}
	for {
		l.skipSpace()
		if l.i >= len(l.b) {
			return nil, io.ErrUnexpectedEOF
		}
		if l.b[l.i] == '>' {
			if l.i+1 < len(l.b) && l.b[l.i+1] == '>' {
				l.i += 2
				break
			}
			return nil, fmt.Errorf("stray '>' in dictionary")
		}
		if l.b[l.i] != '/' {
			return nil, fmt.Errorf("dictionary key is not a name at byte %d", l.i)
		}
		k, err := l.parseName()
		if err != nil {
			return nil, err
		}
		v, err := l.parseObject()
		if err != nil {
			return nil, fmt.Errorf("key /%s: %w", k, err)
		}
		d[k] = v
	}
	// A dictionary followed by the stream keyword owns the bytes after it.
	save := l.i
	l.skipSpace()
	if bytes.HasPrefix(l.b[l.i:], []byte("stream")) {
		l.i += len("stream")
		// The keyword is followed by CRLF or LF, never CR alone.
		if l.i < len(l.b) && l.b[l.i] == '\r' {
			l.i++
		}
		if l.i < len(l.b) && l.b[l.i] == '\n' {
			l.i++
		}
		return stream{d: d, raw: l.b[l.i:]}, nil
	}
	l.i = save
	return d, nil
}

var refPattern = regexp.MustCompile(`^(\d+)[\t\n\f\r ]+(\d+)[\t\n\f\r ]+R\b`)

func (l *lexer) parseKeywordOrNumber() (any, error) {
	start := l.i
	for l.i < len(l.b) && isRegular(l.b[l.i]) {
		l.i++
	}
	if l.i == start {
		return nil, fmt.Errorf("no token at byte %d", l.i)
	}
	tok := string(l.b[start:l.i])
	switch tok {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "null":
		return nil, nil
	}
	// "12 0 R" is an indirect reference; "12" alone is a number. Look ahead
	// before committing.
	if tok[0] >= '0' && tok[0] <= '9' {
		if m := refPattern.FindSubmatch(l.b[start:]); m != nil {
			n, _ := strconv.Atoi(string(m[1]))
			g, _ := strconv.Atoi(string(m[2]))
			l.i = start + len(m[0])
			return ref{n, g}, nil
		}
	}
	f, err := strconv.ParseFloat(tok, 64)
	if err != nil {
		return nil, fmt.Errorf("unknown keyword %q", tok)
	}
	return f, nil
}

// --- document -----------------------------------------------------------

// document is every object in the file, keyed by object number.
type document struct {
	objs map[int]any
}

var objHeader = regexp.MustCompile(`(?m)(?:^|[\r\n\t ])(\d+)[\t\n\f\r ]+(\d+)[\t\n\f\r ]+obj\b`)

// load scans the whole file for "N G obj" headers rather than following the
// cross-reference table. A damaged or unusual xref is exactly the kind of
// thing worth still being able to look at, and every object is wanted here
// anyway.
func load(b []byte) (*document, error) {
	if !bytes.HasPrefix(b, []byte("%PDF-")) {
		return nil, fmt.Errorf("not a PDF: file does not start with %%PDF-")
	}
	d := &document{objs: map[int]any{}}
	// Stream payloads are arbitrary bytes and can contain something that looks
	// like an object header. Skip past each stream's data so that image pixels
	// cannot invent an object and shadow a real one.
	skipUntil := 0
	for _, loc := range objHeader.FindAllSubmatchIndex(b, -1) {
		if loc[0] < skipUntil {
			continue
		}
		num, err := strconv.Atoi(string(b[loc[2]:loc[3]]))
		if err != nil {
			continue
		}
		l := &lexer{b: b, i: loc[1]}
		obj, err := l.parseObject()
		if err != nil {
			// One unreadable object should not blind the whole report; the
			// checks that needed it will say so.
			continue
		}
		if s, ok := obj.(stream); ok {
			// /Length may be an indirect reference to an object not yet seen,
			// so locate the end by keyword instead.
			if i := bytes.Index(s.raw, []byte("endstream")); i >= 0 {
				skipUntil = len(b) - len(s.raw) + i
			}
		}
		// A later definition of the same number wins, which is how an
		// incremental update overrides an earlier object.
		d.objs[num] = obj
	}
	if len(d.objs) == 0 {
		return nil, fmt.Errorf("no PDF objects found")
	}
	if err := d.unpackObjectStreams(); err != nil {
		return nil, err
	}
	return d, nil
}

// resolve follows indirect references to a direct object.
func (d *document) resolve(v any) any {
	for i := 0; i < 32; i++ { // bounded, so a reference cycle cannot hang
		r, ok := v.(ref)
		if !ok {
			return v
		}
		v = d.objs[r.num]
	}
	return nil
}

func (d *document) dict(v any) (dict, bool) {
	switch t := d.resolve(v).(type) {
	case dict:
		return t, true
	case stream:
		return t.d, true
	}
	return nil, false
}

func (d *document) num(v any) (float64, bool) {
	f, ok := d.resolve(v).(float64)
	return f, ok
}

func (d *document) array(v any) (array, bool) {
	a, ok := d.resolve(v).(array)
	return a, ok
}

func (d *document) name(v any) (name, bool) {
	n, ok := d.resolve(v).(name)
	return n, ok
}

// decode returns a stream's data with its filters applied.
func (d *document) decode(s stream) ([]byte, error) {
	raw := s.raw
	// /Length is authoritative, but a missing or indirect one is recoverable
	// by looking for the endstream keyword.
	if n, ok := d.num(s.d["Length"]); ok && int(n) <= len(raw) && n > 0 {
		raw = raw[:int(n)]
	} else if i := bytes.Index(raw, []byte("endstream")); i >= 0 {
		raw = bytes.TrimRight(raw[:i], "\r\n")
	}

	var filters []name
	switch f := d.resolve(s.d["Filter"]).(type) {
	case name:
		filters = []name{f}
	case array:
		for _, v := range f {
			if n, ok := d.name(v); ok {
				filters = append(filters, n)
			}
		}
	}

	for _, f := range filters {
		switch f {
		case "FlateDecode", "Fl":
			zr, err := zlib.NewReader(bytes.NewReader(raw))
			if err != nil {
				return nil, fmt.Errorf("stream is not valid deflate data: %w", err)
			}
			out, err := io.ReadAll(zr)
			if err != nil {
				return nil, fmt.Errorf("inflating stream: %w", err)
			}
			raw = out
		default:
			// DCTDecode and friends are image payloads; this package never
			// needs their pixels, only their dictionaries.
			return nil, fmt.Errorf("unsupported stream filter /%s", f)
		}
	}
	if p := d.resolve(s.d["DecodeParms"]); p != nil && len(filters) > 0 {
		// A predictor rearranges the bytes and would have to be undone before
		// they mean anything. Say so rather than returning scrambled data.
		if pd, ok := d.dict(p); ok {
			if pred, ok := d.num(pd["Predictor"]); ok && pred > 1 {
				return nil, fmt.Errorf("stream uses predictor %d, which is not supported", int(pred))
			}
		}
	}
	return raw, nil
}

// unpackObjectStreams pulls the objects out of any /Type/ObjStm and adds them
// to the document. PDF 1.5 writers — Illustrator and Inkscape among them —
// pack most objects this way, so without this the file looks nearly empty.
func (d *document) unpackObjectStreams() error {
	// Collect first: the loop below adds to d.objs, and ranging over a map
	// while writing to it visits new keys unpredictably.
	var packed []stream
	for _, v := range d.objs {
		s, ok := v.(stream)
		if !ok {
			continue
		}
		if t, _ := d.name(s.d["Type"]); t == "ObjStm" {
			packed = append(packed, s)
		}
	}
	for _, s := range packed {
		data, err := d.decode(s)
		if err != nil {
			return fmt.Errorf("object stream: %w", err)
		}
		n, okN := d.num(s.d["N"])
		first, okF := d.num(s.d["First"])
		if !okN || !okF || int(first) > len(data) {
			return fmt.Errorf("object stream has a bad /N or /First")
		}
		// The head is N pairs of "objectNumber byteOffset".
		head := &lexer{b: data[:int(first)]}
		type entry struct{ num, off int }
		var entries []entry
		for i := 0; i < int(n); i++ {
			a, err1 := head.parseObject()
			b, err2 := head.parseObject()
			if err1 != nil || err2 != nil {
				return fmt.Errorf("object stream header is malformed")
			}
			an, ok1 := a.(float64)
			bn, ok2 := b.(float64)
			if !ok1 || !ok2 {
				return fmt.Errorf("object stream header is not numeric")
			}
			entries = append(entries, entry{int(an), int(bn)})
		}
		for _, e := range entries {
			at := int(first) + e.off
			if at < 0 || at >= len(data) {
				continue
			}
			l := &lexer{b: data, i: at}
			obj, err := l.parseObject()
			if err != nil {
				continue
			}
			// An object written directly in the file overrides the packed
			// copy, so do not clobber what load already found.
			if _, exists := d.objs[e.num]; !exists {
				d.objs[e.num] = obj
			}
		}
	}
	return nil
}
