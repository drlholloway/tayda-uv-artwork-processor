package pdfinspect

import (
	"bytes"
	"fmt"
	"math"
	"regexp"
	"sort"
)

// ptPerMM converts points to millimetres. A PDF user space unit is 1/72 inch.
const mmPerPt = 25.4 / 72.0

// Result is what a PDF turned out to contain.
type Result struct {
	Pages int

	// The first page's MediaBox, in points and in millimetres.
	WidthPt, HeightPt float64
	WidthMM, HeightMM float64

	// SpotColors are the Separation colorant names, deduplicated and sorted.
	//
	// Sorted, not in the order encountered: objects are walked from a map, so
	// there is no encounter order to report and any that looked meaningful
	// would be an accident of iteration. PaintOrder is where ink order lives.
	//
	// Case is preserved: the guide is explicit that RDG_WHITE and Rdg_White
	// are not the same thing.
	SpotColors []string

	// Layers are the optional content group names, deduplicated and sorted,
	// for the same reason.
	Layers []string

	// PaintOrder is the layer names in the order the first page's content
	// stream actually paints them. This is the White → CMYK → Gloss order the
	// guide calls VERY IMPORTANT, read back from the finished file.
	PaintOrder []string

	// ColorSpaces are the colour space names found anywhere in the file, e.g.
	// DeviceCMYK, DeviceGray, DeviceRGB. An ICCBased space is reported with
	// the space its profile declares — "ICCBased RGB" — since the array alone
	// does not say, and RGB hiding inside one is the case that matters.
	ColorSpaces []string

	// Notes records what could not be determined and why. A non-empty Notes
	// means the report is incomplete, not that the file is clean.
	Notes []string
}

// HasRGB reports whether any RGB colour space reached the file. The guide
// requires artwork in CMYK; RGB is converted by the printer's RIP with results
// nobody chose.
//
// It reports the ICCBased case too, which is how RGB actually reaches a file
// in practice: drawing programs write [/ICCBased N 0 R] rather than naming
// /DeviceRGB, and only the component count in the profile gives it away.
func (r Result) HasRGB() bool {
	for _, cs := range r.ColorSpaces {
		switch cs {
		case "DeviceRGB", "CalRGB", "ICCBased RGB":
			return true
		}
	}
	return false
}

// Inspect reads a PDF and reports what it contains.
func Inspect(b []byte) (Result, error) {
	var r Result
	d, err := load(b)
	if err != nil {
		return r, err
	}
	if _, encrypted := d.findEncrypt(); encrypted {
		return r, fmt.Errorf("PDF is encrypted; nothing can be checked")
	}

	pages := d.pages()
	r.Pages = len(pages)
	if len(pages) == 0 {
		return r, fmt.Errorf("no pages found; this does not look like a PDF page tree")
	}

	if box, ok := d.mediaBox(pages[0]); ok {
		r.WidthPt, r.HeightPt = box[0], box[1]
		r.WidthMM = box[0] * mmPerPt
		r.HeightMM = box[1] * mmPerPt
	} else {
		r.Notes = append(r.Notes, "the first page has no readable /MediaBox, so the artboard size is unknown")
	}

	r.SpotColors = d.spotColors()
	r.Layers = d.layerNames()

	spaces, csNotes := d.colorSpaces()
	r.ColorSpaces = spaces
	r.Notes = append(r.Notes, csNotes...)

	order, err := d.paintOrder(pages[0])
	if err != nil {
		r.Notes = append(r.Notes, fmt.Sprintf("could not read the paint order: %v", err))
	} else {
		r.PaintOrder = order
	}
	return r, nil
}

func (d *document) findEncrypt() (dict, bool) {
	// The trailer is not parsed here, so look for the encryption dictionary's
	// distinctive shape among the objects.
	for _, v := range d.objs {
		if dd, ok := v.(dict); ok {
			if _, hasFilter := dd["Filter"]; hasFilter {
				if _, hasV := dd["V"]; hasV {
					if _, hasR := dd["R"]; hasR {
						return dd, true
					}
				}
			}
		}
	}
	return nil, false
}

// pages returns the page dictionaries, walking the page tree from the catalog
// when there is one and falling back to every /Type/Page object otherwise.
func (d *document) pages() []dict {
	for _, v := range d.objs {
		dd, ok := v.(dict)
		if !ok {
			continue
		}
		if t, _ := d.name(dd["Type"]); t != "Catalog" {
			continue
		}
		if root, ok := d.dict(dd["Pages"]); ok {
			if out := d.walkPages(root, 0, map[int]bool{}); len(out) > 0 {
				return out
			}
		}
	}

	// No usable catalog. Collect loose page objects in object-number order so
	// the result is at least deterministic.
	var nums []int
	for n, v := range d.objs {
		if dd, ok := v.(dict); ok {
			if t, _ := d.name(dd["Type"]); t == "Page" {
				nums = append(nums, n)
			}
		}
	}
	sort.Ints(nums)
	var out []dict
	for _, n := range nums {
		out = append(out, d.objs[n].(dict))
	}
	return out
}

// walkPages collects the page dictionaries beneath a node.
//
// visited holds the object numbers already entered on this walk. The depth
// cap alone bounds how deep the recursion goes but not how much work it does:
// a node listing itself twice doubles the work at every level, so a tree
// thirty deep is a billion visits and the tool simply stops responding. A
// damaged file is the one this package promises to still be able to look at,
// so it must not be the one that hangs it.
func (d *document) walkPages(node dict, depth int, visited map[int]bool) []dict {
	if depth > 32 {
		return nil
	}
	t, _ := d.name(node["Type"])
	if t == "Page" {
		return []dict{node}
	}
	kids, ok := d.array(node["Kids"])
	if !ok {
		return nil
	}
	var out []dict
	for _, k := range kids {
		// Checked before resolving, since a cycle can only be formed by
		// reference; a dictionary written inline cannot name itself.
		if r, isRef := k.(ref); isRef {
			if visited[r.num] {
				continue
			}
			visited[r.num] = true
		}
		kd, ok := d.dict(k)
		if !ok {
			continue
		}
		// Inheritable attributes pass from a Pages node down to its children.
		for _, attr := range []name{"MediaBox", "Resources", "Rotate", "CropBox"} {
			if _, set := kd[attr]; !set {
				if v, has := node[attr]; has {
					kd[attr] = v
				}
			}
		}
		out = append(out, d.walkPages(kd, depth+1, visited)...)
	}
	return out
}

// mediaBox returns the page's width and height in points, normalised so that
// the box need not start at the origin.
func (d *document) mediaBox(page dict) ([2]float64, bool) {
	a, ok := d.array(page["MediaBox"])
	if !ok || len(a) != 4 {
		return [2]float64{}, false
	}
	var v [4]float64
	for i := range v {
		f, ok := d.num(a[i])
		if !ok {
			return [2]float64{}, false
		}
		v[i] = f
	}
	w, h := v[2]-v[0], v[3]-v[1]
	if w < 0 {
		w = -w
	}
	if h < 0 {
		h = -h
	}

	// /Rotate turns the page for display, so a quarter turn swaps what the
	// artboard measures. walkPages already inherits the key down the tree;
	// ignoring it here reported a rotated page's sides the wrong way round,
	// and enclosure.MatchSize then named the wrong side with full confidence
	// — the ruined-enclosure mistake this tool exists to catch.
	//
	// math.Mod rather than an int conversion: /Rotate comes from the file and
	// need not be a sane number, and NaN must fall through both cases.
	if rot, ok := d.num(page["Rotate"]); ok {
		deg := math.Mod(rot, 360)
		if deg < 0 {
			deg += 360
		}
		if deg == 90 || deg == 270 {
			w, h = h, w
		}
	}
	return [2]float64{w, h}, true
}

// spotColors finds every Separation colorant name. A Separation colour space
// is the array [/Separation /Name altSpace tintTransform]; DeviceN carries a
// list of names instead.
func (d *document) spotColors() []string {
	var out []string
	seen := map[string]bool{}
	add := func(n name) {
		// /All and /None are reserved colorant names, not spot inks.
		if n == "All" || n == "None" || seen[string(n)] {
			return
		}
		seen[string(n)] = true
		out = append(out, string(n))
	}
	for _, v := range d.objs {
		d.eachArray(v, func(a array) {
			if len(a) < 2 {
				return
			}
			kind, ok := d.name(a[0])
			if !ok {
				return
			}
			switch kind {
			case "Separation":
				if n, ok := d.name(a[1]); ok {
					add(n)
				}
			case "DeviceN":
				if names, ok := d.array(a[1]); ok {
					for _, nv := range names {
						if n, ok := d.name(nv); ok {
							add(n)
						}
					}
				}
			}
		})
	}
	sort.Strings(out)
	return out
}

// eachArray calls fn for every array reachable inside v, including v itself.
func (d *document) eachArray(v any, fn func(array)) {
	d.each(v, 0, func(x any) {
		if a, ok := x.(array); ok {
			fn(a)
		}
	})
}

func (d *document) each(v any, depth int, fn func(any)) {
	if depth > 32 {
		return
	}
	switch t := v.(type) {
	case stream:
		fn(t.d)
		for _, sub := range t.d {
			d.each(sub, depth+1, fn)
		}
	case dict:
		fn(t)
		for _, sub := range t {
			d.each(sub, depth+1, fn)
		}
	case array:
		fn(t)
		for _, sub := range t {
			d.each(sub, depth+1, fn)
		}
	default:
		fn(v)
	}
}

// layerNames returns the optional content group names.
func (d *document) layerNames() []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range d.objs {
		dd, ok := v.(dict)
		if !ok {
			continue
		}
		if t, _ := d.name(dd["Type"]); t != "OCG" {
			continue
		}
		if s, ok := d.resolve(dd["Name"]).(str); ok && !seen[string(s)] {
			seen[string(s)] = true
			out = append(out, string(s))
		}
	}
	sort.Strings(out)
	return out
}

// colorSpaces reports the colour spaces named anywhere in the file, both in
// object dictionaries and in content streams, along with anything it could
// not read well enough to be sure about.
func (d *document) colorSpaces() (spaces, notes []string) {
	known := []string{"DeviceCMYK", "DeviceRGB", "DeviceGray", "CalRGB", "CalGray", "Lab", "ICCBased", "Indexed"}
	found := map[string]bool{}
	unreadable := map[string]bool{}

	for _, v := range d.objs {
		d.each(v, 0, func(x any) {
			if n, ok := x.(name); ok {
				for _, k := range known {
					if string(n) == k {
						found[k] = true
					}
				}
			}
		})
		// An ICCBased space says nothing about itself; only the component
		// count in its profile stream distinguishes RGB from CMYK. This is
		// the form real RGB artwork arrives in — Illustrator, Inkscape and
		// Acrobat all write [/ICCBased N 0 R] rather than /DeviceRGB — so
		// without resolving it an entirely RGB file passed as "no RGB".
		d.eachArray(v, func(a array) {
			if len(a) < 2 {
				return
			}
			if k, ok := d.name(a[0]); !ok || k != "ICCBased" {
				return
			}
			s, ok := d.resolve(a[1]).(stream)
			if !ok {
				unreadable["an ICCBased colour space has no readable profile, so whether it is RGB is unknown"] = true
				return
			}
			n, ok := d.num(s.d["N"])
			if !ok {
				unreadable["an ICCBased profile has no /N, so whether it is RGB is unknown"] = true
				return
			}
			if nm := iccSpaceName(n); nm != "" {
				found[nm] = true
			} else {
				unreadable[fmt.Sprintf("an ICCBased profile declares %g components, which is not a colour space this knows", n)] = true
			}
		})

		// Content streams name colour spaces as operands, e.g. "/DeviceRGB cs".
		if s, ok := v.(stream); ok {
			if t, _ := d.name(s.d["Type"]); t == "ObjStm" {
				continue
			}
			data, err := d.decode(s)
			if err != nil {
				// Silence here would be a verdict about bytes never read, and
				// an unsupported filter or predictor is exactly where a stray
				// "/DeviceRGB cs" would sit unseen.
				unreadable[fmt.Sprintf("a stream could not be decoded (%v), so any colour space inside it was not seen", err)] = true
				continue
			}
			for _, k := range known {
				if bytes.Contains(data, []byte("/"+k)) {
					found[k] = true
				}
			}
		}
	}

	for k := range found {
		spaces = append(spaces, k)
	}
	sort.Strings(spaces)
	for k := range unreadable {
		notes = append(notes, k)
	}
	sort.Strings(notes)
	return spaces, notes
}

// iccSpaceName names an ICCBased space by its component count, which is the
// only thing in the file that says which one it is. An unrecognised count
// returns "" so the caller can say so rather than guess.
func iccSpaceName(components float64) string {
	switch components {
	case 1:
		return "ICCBased Gray"
	case 3:
		return "ICCBased RGB"
	case 4:
		return "ICCBased CMYK"
	}
	return ""
}

var bdcPattern = regexp.MustCompile(`/OC\s*/([^\s/\[\]<>()]+)\s+BDC`)

// paintOrder reads the page's content stream and returns the layer names in
// the order they are painted, resolving the short resource names through the
// page's /Properties dictionary.
func (d *document) paintOrder(page dict) ([]string, error) {
	content, err := d.pageContent(page)
	if err != nil {
		return nil, err
	}

	res, ok := d.dict(page["Resources"])
	if !ok {
		return nil, fmt.Errorf("page has no /Resources")
	}
	props, ok := d.dict(res["Properties"])
	if !ok {
		// No properties means no marked optional content; an unlayered file
		// is unusual for this tool but not unreadable.
		return nil, nil
	}
	byResource := map[string]string{}
	for k, v := range props {
		if ocg, ok := d.dict(v); ok {
			if s, ok := d.resolve(ocg["Name"]).(str); ok {
				byResource[string(k)] = string(s)
			}
		}
	}

	var out []string
	for _, m := range bdcPattern.FindAllSubmatch(content, -1) {
		// The captured token is raw content-stream bytes, while the
		// /Properties keys came through the parser already decoded. Without
		// decoding it too, /OC /RDG#5FW BDC never matches its own entry, the
		// fallback reports the undecoded token as the layer name, and the
		// order check then compares against a name no OCG has and passes.
		// This package treats #xx decoding as load-bearing everywhere else.
		res := decodeNameToken(m[1])
		if n, ok := byResource[res]; ok {
			out = append(out, n)
		} else {
			out = append(out, res)
		}
	}
	return out, nil
}

// decodeNameToken applies the #xx escapes a name written in a content stream
// may carry. It runs the token through the same lexer the object parser uses,
// so the two cannot decode a name differently.
func decodeNameToken(token []byte) string {
	l := &lexer{b: append([]byte{'/'}, token...)}
	n, err := l.parseName()
	if err != nil {
		return string(token)
	}
	return string(n)
}

// pageContent concatenates the page's content streams, which /Contents may
// hold either singly or as an array.
func (d *document) pageContent(page dict) ([]byte, error) {
	var parts [][]byte
	switch t := d.resolve(page["Contents"]).(type) {
	case stream:
		data, err := d.decode(t)
		if err != nil {
			return nil, err
		}
		parts = append(parts, data)
	case array:
		for i, v := range t {
			// Skipping an entry would hand back a short content stream that
			// reads as complete: the paint order comes back missing whichever
			// layers lived in the part that was dropped, and a missing CMYK
			// layer is reported as a clean White → Gloss order rather than as
			// a file that could not be read.
			s, ok := d.resolve(v).(stream)
			if !ok {
				return nil, fmt.Errorf("/Contents entry %d is not a stream, so the paint order cannot be read in full", i)
			}
			data, err := d.decode(s)
			if err != nil {
				return nil, fmt.Errorf("/Contents entry %d: %w", i, err)
			}
			parts = append(parts, data)
		}
	default:
		return nil, fmt.Errorf("page has no /Contents stream")
	}
	return bytes.Join(parts, []byte("\n")), nil
}
