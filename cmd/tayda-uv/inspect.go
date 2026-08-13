package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/laneholloway/tayda-uv-artwork-processor/internal/enclosure"
	"github.com/laneholloway/tayda-uv-artwork-processor/internal/pdfgen"
	"github.com/laneholloway/tayda-uv-artwork-processor/internal/pdfinspect"
)

type inspectOpts struct {
	enclosure string
	side      string
}

func inspectFlags(o *inspectOpts) *flag.FlagSet {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	fs.StringVar(&o.enclosure, "e", "", "assert the artboard is this enclosure's")
	fs.StringVar(&o.side, "s", "", "assert the artboard is this side's")
	return fs
}

func cmdInspect(args []string) error {
	var o inspectOpts
	fs := inspectFlags(&o)
	helped, err := setup("inspect", fs, args)
	if helped || err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return &usageError{cmd: "inspect", msg: "inspect needs at least one PDF"}
	}
	// Asserting a side means naming both halves of it.
	if (o.enclosure == "") != (o.side == "") {
		return &usageError{cmd: "inspect", msg: "-e and -s go together: give both, or neither"}
	}

	var want *enclosure.Size
	var wantName string
	if o.enclosure != "" {
		size, side, err := resolve("inspect", o.enclosure, o.side)
		if err != nil {
			return err
		}
		want, wantName = &size, fmt.Sprintf("%s side %s", o.enclosure, side)
	}

	bad := 0
	for i, path := range fs.Args() {
		if i > 0 {
			fmt.Println()
		}
		ok, err := inspectOne(path, want, wantName)
		if err != nil {
			// An unreadable file is a failure of this file, not of the run;
			// keep going so one bad PDF does not hide the rest.
			fmt.Printf("%s: %v\n", path, err)
			bad++
			continue
		}
		if !ok {
			bad++
		}
	}

	if bad > 0 {
		return fmt.Errorf("%s did not pass inspection", plural(bad, "file", "files"))
	}
	return nil
}

func inspectOne(path string, want *enclosure.Size, wantName string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	r, err := pdfinspect.Inspect(b)
	if err != nil {
		return false, err
	}

	var problems, warnings []string

	fmt.Printf("%s: %s, %s × %s pt\n", path,
		plural(r.Pages, "page", "pages"), trim(r.WidthPt), trim(r.HeightPt))

	// --- artboard -------------------------------------------------------
	if r.Pages != 1 {
		problems = append(problems, fmt.Sprintf(
			"the file holds %d pages; the Tayda Box Tool expects one artboard per PDF", r.Pages))
	}
	size := fmt.Sprintf("%s × %s mm", trim(r.WidthMM), trim(r.HeightMM))
	switch {
	case r.WidthMM == 0 || r.HeightMM == 0:
		problems = append(problems, "the artboard size could not be read")
	case want != nil:
		if near(r.WidthMM, want.WidthMM) && near(r.HeightMM, want.HeightMM) {
			fmt.Printf("  artboard: %-24s %s OK\n", size, wantName)
		} else {
			fmt.Printf("  artboard: %s\n", size)
			problems = append(problems, fmt.Sprintf(
				"artboard is %s but %s is %s", size, wantName, want))
		}
	default:
		matches := enclosure.MatchSize(r.WidthMM, r.HeightMM)
		if len(matches) == 0 {
			fmt.Printf("  artboard: %s\n", size)
			problems = append(problems, fmt.Sprintf(
				"artboard %s matches no known enclosure side", size))
		} else {
			fmt.Printf("  artboard: %-24s %s\n", size, matchNames(matches))
		}
	}

	// --- colour ---------------------------------------------------------
	spaces := strings.Join(r.ColorSpaces, ", ")
	if spaces == "" {
		spaces = "none named"
	}
	if r.HasRGB() {
		fmt.Printf("  colour:   %s\n", spaces)
		problems = append(problems, "RGB reached the file; the guide requires CMYK artwork")
	} else {
		fmt.Printf("  colour:   %-24s no RGB OK\n", spaces)
	}

	// --- spot inks ------------------------------------------------------
	if len(r.SpotColors) == 0 {
		fmt.Printf("  spots:    none\n")
		warnings = append(warnings,
			"no spot colours, so no white undercoat and no varnish: right only for a white enclosure")
	} else {
		var wrong []string
		for _, s := range r.SpotColors {
			if !knownSpot(s) {
				wrong = append(wrong, s)
			}
		}
		if len(wrong) == 0 {
			fmt.Printf("  spots:    %-24s OK\n", strings.Join(r.SpotColors, ", "))
		} else {
			fmt.Printf("  spots:    %s\n", strings.Join(r.SpotColors, ", "))
			for _, s := range wrong {
				problems = append(problems, spotProblem(s))
			}
		}
	}

	// --- print order ----------------------------------------------------
	if len(r.PaintOrder) > 0 {
		order := strings.Join(r.PaintOrder, " → ")
		if msg := orderProblem(r.PaintOrder); msg != "" {
			fmt.Printf("  order:    %s\n", order)
			problems = append(problems, msg)
		} else {
			fmt.Printf("  order:    %-24s OK\n", order)
		}
	} else if len(r.Layers) > 0 {
		warnings = append(warnings, "the layers are not marked in the content stream, so the print order could not be read")
	}

	for _, n := range r.Notes {
		warnings = append(warnings, n)
	}

	for _, w := range warnings {
		fmt.Printf("  warning: %s\n", w)
	}
	for _, p := range problems {
		fmt.Printf("  problem: %s\n", p)
	}
	return len(problems) == 0, nil
}

// knownSpot reports whether a Separation name is one the Tayda printer acts
// on. Anything else is ink the press will not lay down.
func knownSpot(s string) bool {
	return s == pdfgen.SpotWhite || s == pdfgen.SpotGloss
}

// spotProblem explains a spot name the press will not recognise, calling out
// the case mistake specifically because the guide singles it out.
func spotProblem(s string) string {
	for _, want := range []string{pdfgen.SpotWhite, pdfgen.SpotGloss} {
		if strings.EqualFold(s, want) {
			return fmt.Sprintf(
				"spot colour is named %q, but the name is case-sensitive and must be exactly %q", s, want)
		}
	}
	return fmt.Sprintf(
		"spot colour %q is not one the printer acts on (%s or %s)", s, pdfgen.SpotWhite, pdfgen.SpotGloss)
}

// orderProblem checks the White → CMYK → Gloss sequence the guide's checklist
// calls VERY IMPORTANT. White laid over the artwork hides it; varnish under
// the artwork does nothing.
func orderProblem(order []string) string {
	pos := func(name string) int {
		for i, n := range order {
			if n == name {
				return i
			}
		}
		return -1
	}
	white, cmyk, gloss := pos(pdfgen.SpotWhite), pos(pdfgen.LayerCMYK), pos(pdfgen.SpotGloss)
	if cmyk < 0 {
		return ""
	}
	if white >= 0 && white > cmyk {
		return fmt.Sprintf("%s is painted after %s; the undercoat must go down first or it hides the artwork",
			pdfgen.SpotWhite, pdfgen.LayerCMYK)
	}
	if gloss >= 0 && gloss < cmyk {
		return fmt.Sprintf("%s is painted before %s; varnish goes on last",
			pdfgen.SpotGloss, pdfgen.LayerCMYK)
	}
	return ""
}

func matchNames(m []enclosure.Match) string {
	parts := make([]string, len(m))
	for i, x := range m {
		parts[i] = x.String()
	}
	return strings.Join(parts, ", ")
}

// near allows the rounding that a trip through PDF points and back introduces.
func near(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= enclosure.MatchSizeEpsilonMM
}

// trim formats a measurement without trailing zeroes, so 56 stays 56 and
// 108.5 stays 108.5.
func trim(f float64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", f), "0"), ".")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
