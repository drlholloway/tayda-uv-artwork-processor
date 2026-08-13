package enclosure

import "testing"

// The guide's table is the contract with the print service, so spot-check
// values transcribed from it rather than trusting the map to stay untouched.
func TestCatalogMatchesGuide(t *testing.T) {
	cases := []struct {
		enclosure string
		side      Side
		want      Size
	}{
		{"1590B", SideA, Size{56, 108.50}},
		{"1590B", SideLid, Size{56, 108.50}},
		{"1590A", SideB, Size{30, 25}},
		{"125B", SideC, Size{33, 111}},
		{"1590BB2", SideB, Size{84, 32}}, // differs from 1590BB only on B/C/D/E
		{"1590BB", SideB, Size{84, 29}},
		{"1590DD", SideE, Size{29, 179}},
		{"1590D", SideA, Size{113, 182}},
		{"1590XX", SideLid, Size{117, 141}},
	}
	for _, tc := range cases {
		e, err := Lookup(tc.enclosure)
		if err != nil {
			t.Fatalf("Lookup(%q): %v", tc.enclosure, err)
		}
		got, err := e.Size(tc.side)
		if err != nil {
			t.Fatalf("%s side %s: %v", tc.enclosure, tc.side, err)
		}
		if got != tc.want {
			t.Errorf("%s side %s = %v, want %v", tc.enclosure, tc.side, got, tc.want)
		}
	}
}

// Side A and the Lid are the same plate size on every enclosure in the guide.
func TestFaceAndLidMatch(t *testing.T) {
	for _, name := range Names() {
		e, err := Lookup(name)
		if err != nil {
			t.Fatal(err)
		}
		a, _ := e.Size(SideA)
		lid, _ := e.Size(SideLid)
		if a != lid {
			t.Errorf("%s: side A %v != lid %v", name, a, lid)
		}
	}
}

// B/D and C/E are opposing sides and share a size on every enclosure.
func TestOpposingSidesMatch(t *testing.T) {
	for _, name := range Names() {
		e, _ := Lookup(name)
		b, _ := e.Size(SideB)
		d, _ := e.Size(SideD)
		c, _ := e.Size(SideC)
		en, _ := e.Size(SideE)
		if b != d {
			t.Errorf("%s: side B %v != side D %v", name, b, d)
		}
		if c != en {
			t.Errorf("%s: side C %v != side E %v", name, c, en)
		}
	}
}

func TestEveryEnclosureHasEverySide(t *testing.T) {
	names := Names()
	if len(names) != 8 {
		t.Errorf("got %d enclosures, guide lists 8: %v", len(names), names)
	}
	for _, name := range names {
		e, _ := Lookup(name)
		for _, s := range Sides {
			if _, err := e.Size(s); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		}
	}
}

func TestLookupIsCaseInsensitive(t *testing.T) {
	for _, name := range []string{"1590b", "1590B", " 1590B "} {
		if _, err := Lookup(name); err != nil {
			t.Errorf("Lookup(%q): %v", name, err)
		}
	}
	if _, err := Lookup("1590Z"); err == nil {
		t.Error("Lookup(1590Z) should fail")
	}
}

func TestParseSide(t *testing.T) {
	for in, want := range map[string]Side{"a": SideA, "A": SideA, "lid": SideLid, "LID": SideLid} {
		got, err := ParseSide(in)
		if err != nil {
			t.Errorf("ParseSide(%q): %v", in, err)
		} else if got != want {
			t.Errorf("ParseSide(%q) = %v, want %v", in, got, want)
		}
	}
	if _, err := ParseSide("F"); err == nil {
		t.Error("ParseSide(F) should fail")
	}
}

func TestTolerance(t *testing.T) {
	if got := ToleranceMM(SideA); got != 0.50 {
		t.Errorf("side A tolerance = %v, want 0.50", got)
	}
	if got := ToleranceMM(SideLid); got != 1.00 {
		t.Errorf("lid tolerance = %v, want 1.00", got)
	}
}
