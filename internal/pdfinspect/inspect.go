package pdfinspect

import (
	"bytes"
	"fmt"
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

	// SpotColors are the Separation colorant names, in the order encountered,
	// deduplicated. Case is preserved: the guide is explicit that RDG_WHITE
	// and Rdg_White are not the same thing.
	SpotColors []string

	// Layers are the optional content group names.
	Layers []string

	// PaintOrder is the layer names in the order the first page's content
	// stream actually paints them. This is the White → CMYK → Gloss order the
	// guide calls VERY IMPORTANT, read back from the finished file.
	PaintOrder []string

	// ColorSpaces are the device colour space names found anywhere in the
	// file, e.g. DeviceCMYK, DeviceGray, DeviceRGB.
	ColorSpaces []string

	// Notes records what could not be determined and why. A non-empty Notes
	// means the report is incomplete, not that the file is clean.
	Notes []string
}

// HasRGB reports whether any RGB colour space reached the file. The guide
// requires artwork in CMYK; RGB is converted by the printer's RIP with results
// nobody chose.
func (r Result) HasRGB() bool {
	for _, cs := range r.ColorSpaces {
		if cs == "DeviceRGB" || cs == "CalRGB" {
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
	r.ColorSpaces = d.colorSpaces()

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
			if out := d.walkPages(root, 0); len(out) > 0 {
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

func (d *document) walkPages(node dict, depth int) []dict {
	if depth > 32 { // a malformed tree must not recurse forever
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
		out = append(out, d.walkPages(kd, depth+1)...)
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

// colorSpaces reports the device colour spaces named anywhere in the file,
// both in object dictionaries and in content streams.
func (d *document) colorSpaces() []string {
	known := []string{"DeviceCMYK", "DeviceRGB", "DeviceGray", "CalRGB", "CalGray", "Lab", "ICCBased", "Indexed"}
	found := map[string]bool{}

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
		// Content streams name colour spaces as operands, e.g. "/DeviceRGB cs".
		if s, ok := v.(stream); ok {
			if t, _ := d.name(s.d["Type"]); t == "ObjStm" {
				continue
			}
			data, err := d.decode(s)
			if err != nil {
				continue
			}
			for _, k := range known {
				if bytes.Contains(data, []byte("/"+k)) {
					found[k] = true
				}
			}
		}
	}

	var out []string
	for k := range found {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
		if n, ok := byResource[string(m[1])]; ok {
			out = append(out, n)
		} else {
			out = append(out, string(m[1]))
		}
	}
	return out, nil
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
		for _, v := range t {
			s, ok := d.resolve(v).(stream)
			if !ok {
				continue
			}
			data, err := d.decode(s)
			if err != nil {
				return nil, err
			}
			parts = append(parts, data)
		}
	default:
		return nil, fmt.Errorf("page has no /Contents stream")
	}
	return bytes.Join(parts, []byte("\n")), nil
}
