// Package enclosure holds the artboard dimensions Tayda requires for each
// enclosure side.
//
// Every number here is transcribed from the enclosure table in the Tayda UV
// Printing Service File Preparation Guide (V2, April 22 2026):
// https://www.taydaelectronics.com/uv-printing-service-guide-v1
//
// Tayda prints files exactly as submitted and does not inspect or correct
// them, so an artboard that does not match these numbers exactly produces a
// misaligned print at the customer's expense. Treat this table as ground
// truth and re-check it against the guide before changing anything.
package enclosure

import (
	"fmt"
	"sort"
	"strings"
)

// Side identifies one printable face of an enclosure. Side A is the face,
// Lid is the back plate that screws on.
type Side string

const (
	SideA   Side = "A"
	SideB   Side = "B"
	SideC   Side = "C"
	SideD   Side = "D"
	SideE   Side = "E"
	SideLid Side = "Lid"
)

// Sides lists every side in the order the guide's table presents them.
var Sides = []Side{SideA, SideB, SideC, SideD, SideE, SideLid}

// Size is an artboard size in millimetres, stated as the guide states it:
// width by height, in the orientation the guide's "THIS WAY UP" diagram shows.
type Size struct {
	WidthMM  float64
	HeightMM float64
}

func (s Size) String() string {
	return fmt.Sprintf("%g × %g mm", s.WidthMM, s.HeightMM)
}

// AspectRatio is width divided by height.
func (s Size) AspectRatio() float64 { return s.WidthMM / s.HeightMM }

// Enclosure is one drilled box Tayda will print on.
type Enclosure struct {
	Name  string
	sides map[Side]Size
}

// Size returns the artboard size for a side.
func (e Enclosure) Size(s Side) (Size, error) {
	sz, ok := e.sides[s]
	if !ok {
		return Size{}, fmt.Errorf("enclosure %s has no side %q (sides are %s)", e.Name, s, joinSides(Sides))
	}
	return sz, nil
}

// ToleranceMM is the printing tolerance Tayda quotes for a side: ±0.50 mm on
// side A (the face), ±1.00 mm elsewhere. It is the registration accuracy of
// their printer, not a licence to submit a mis-sized artboard.
func ToleranceMM(s Side) float64 {
	if s == SideA {
		return 0.50
	}
	return 1.00
}

// catalog is keyed by the lowercased enclosure name; use Lookup to read it.
var catalog = map[string]Enclosure{}

func register(name string, a, b, c, d, e, lid Size) {
	catalog[strings.ToLower(name)] = Enclosure{
		Name: name,
		sides: map[Side]Size{
			SideA: a, SideB: b, SideC: c, SideD: d, SideE: e, SideLid: lid,
		},
	}
}

func init() {
	// ENCLOSURE   SIDE A          SIDE B      SIDE C      SIDE D      SIDE E      LID
	register("125B", Size{62, 117}, Size{57, 33}, Size{33, 111}, Size{57, 33}, Size{33, 111}, Size{62, 117})
	register("1590A", Size{35, 89}, Size{30, 25}, Size{25, 83}, Size{30, 25}, Size{25, 83}, Size{35, 89})
	register("1590B", Size{56, 108.50}, Size{52, 24}, Size{24, 103}, Size{52, 24}, Size{24, 103}, Size{56, 108.50})
	register("1590BB", Size{90, 115.50}, Size{84, 29}, Size{29, 110}, Size{84, 29}, Size{29, 110}, Size{90, 115.50})
	register("1590BB2", Size{90, 115.50}, Size{84, 32}, Size{32, 110}, Size{84, 32}, Size{32, 110}, Size{90, 115.50})
	register("1590XX", Size{117, 141}, Size{112, 32}, Size{32, 135}, Size{112, 32}, Size{32, 135}, Size{117, 141})
	register("1590DD", Size{117, 185}, Size{110, 29}, Size{29, 179}, Size{110, 29}, Size{29, 179}, Size{117, 185})
	register("1590D", Size{113, 182}, Size{105, 48}, Size{48, 172}, Size{105, 48}, Size{48, 172}, Size{113, 182})
}

// Lookup finds an enclosure by name, case-insensitively.
func Lookup(name string) (Enclosure, error) {
	e, ok := catalog[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return Enclosure{}, fmt.Errorf("unknown enclosure %q (known: %s)", name, strings.Join(Names(), ", "))
	}
	return e, nil
}

// ParseSide accepts a side name case-insensitively, so "lid" and "Lid" and
// "a" and "A" all work from a command line.
func ParseSide(s string) (Side, error) {
	want := strings.ToLower(strings.TrimSpace(s))
	for _, side := range Sides {
		if strings.ToLower(string(side)) == want {
			return side, nil
		}
	}
	return "", fmt.Errorf("unknown side %q (sides are %s)", s, joinSides(Sides))
}

// Names lists every known enclosure, sorted for stable output.
func Names() []string {
	out := make([]string, 0, len(catalog))
	for _, e := range catalog {
		out = append(out, e.Name)
	}
	sort.Strings(out)
	return out
}

func joinSides(sides []Side) string {
	parts := make([]string, len(sides))
	for i, s := range sides {
		parts[i] = string(s)
	}
	return strings.Join(parts, ", ")
}
