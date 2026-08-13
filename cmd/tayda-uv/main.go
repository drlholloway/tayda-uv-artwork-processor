// Command tayda-uv prepares artwork for the Tayda UV printing service.
//
// Every operation is reachable from the command line so the tool can be
// driven by a script or an agent, not only by a human at a terminal.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/laneholloway/tayda-uv-artwork-processor/internal/artwork"
	"github.com/laneholloway/tayda-uv-artwork-processor/internal/enclosure"
	"github.com/laneholloway/tayda-uv-artwork-processor/internal/pdfgen"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "enclosures":
		err = cmdEnclosures()
	case "sides":
		err = cmdSides(os.Args[2:])
	case "validate":
		err = cmdValidate(os.Args[2:])
	case "convert":
		err = cmdConvert(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `tayda-uv — prepare artwork for the Tayda UV printing service

Usage:
  tayda-uv enclosures                       list supported enclosures
  tayda-uv sides <enclosure>                show artboard sizes for each side
  tayda-uv validate -e <enc> -s <side> <image>
                                            check artwork without writing a file
  tayda-uv convert  -e <enc> -s <side> [-white auto|none|full] [-o out.pdf] <image>
                                            write a print-ready PDF

Examples:
  tayda-uv sides 1590B
  tayda-uv validate -e 1590B -s A face.png
  tayda-uv convert -e 1590B -s A -white full -o face.pdf face.png

Artboard sizes come from the Tayda UV Printing Service File Preparation Guide.
Tayda prints files exactly as submitted, so check the result before ordering in
quantity — their PDF Analyzer tool is the final word.
`)
}

func cmdEnclosures() error {
	for _, name := range enclosure.Names() {
		fmt.Println(name)
	}
	return nil
}

func cmdSides(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: tayda-uv sides <enclosure>")
	}
	e, err := enclosure.Lookup(args[0])
	if err != nil {
		return err
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SIDE\tARTBOARD\tMIN PIXELS @300DPI\tTOLERANCE")
	for _, s := range enclosure.Sides {
		sz, err := e.Size(s)
		if err != nil {
			return err
		}
		w, h := artwork.RequiredPixels(sz)
		fmt.Fprintf(tw, "%s\t%g × %g mm\t%d × %d\t±%.2f mm\n",
			s, sz.WidthMM, sz.HeightMM, w, h, enclosure.ToleranceMM(s))
	}
	return tw.Flush()
}

// target resolves the -e/-s flags shared by validate and convert.
func target(fs *flag.FlagSet, encName, sideName string) (enclosure.Size, enclosure.Side, error) {
	if encName == "" || sideName == "" {
		fs.Usage()
		return enclosure.Size{}, "", fmt.Errorf("both -e and -s are required")
	}
	e, err := enclosure.Lookup(encName)
	if err != nil {
		return enclosure.Size{}, "", err
	}
	side, err := enclosure.ParseSide(sideName)
	if err != nil {
		return enclosure.Size{}, "", err
	}
	sz, err := e.Size(side)
	return sz, side, err
}

func cmdValidate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	encName := fs.String("e", "", "enclosure name, e.g. 1590B")
	sideName := fs.String("s", "", "side: A, B, C, D, E or Lid")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: tayda-uv validate -e <enclosure> -s <side> <image>")
	}

	size, side, err := target(fs, *encName, *sideName)
	if err != nil {
		return err
	}
	img, err := artwork.Load(fs.Arg(0))
	if err != nil {
		return err
	}

	report := artwork.Check(img, side, size)
	printReport(img, report)
	if !report.OK() {
		return fmt.Errorf("artwork is not ready for printing")
	}
	return nil
}

func cmdConvert(args []string) error {
	fs := flag.NewFlagSet("convert", flag.ExitOnError)
	encName := fs.String("e", "", "enclosure name, e.g. 1590B")
	sideName := fs.String("s", "", "side: A, B, C, D, E or Lid")
	whiteName := fs.String("white", "auto", "RDG_WHITE undercoat: none, auto or full")
	outPath := fs.String("o", "", "output PDF (default: <image>-<enclosure>-<side>.pdf)")
	force := fs.Bool("force", false, "write the PDF even if validation finds problems")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: tayda-uv convert -e <enclosure> -s <side> <image>")
	}

	size, side, err := target(fs, *encName, *sideName)
	if err != nil {
		return err
	}
	white, err := pdfgen.ParseWhiteMode(*whiteName)
	if err != nil {
		return err
	}
	in := fs.Arg(0)
	img, err := artwork.Load(in)
	if err != nil {
		return err
	}

	report := artwork.Check(img, side, size)
	printReport(img, report)
	if !report.OK() && !*force {
		return fmt.Errorf("refusing to write a PDF that will not print correctly (use -force to override)")
	}

	out := *outPath
	if out == "" {
		base := strings.TrimSuffix(filepath.Base(in), filepath.Ext(in))
		out = fmt.Sprintf("%s-%s-%s.pdf", base, strings.ToLower(*encName), strings.ToLower(string(side)))
	}

	f, err := os.Create(out)
	if err != nil {
		return err
	}
	// Close explicitly so a write error on flush is not swallowed, but keep a
	// deferred close for the error paths.
	defer f.Close()

	job := pdfgen.Job{Image: img, WidthMM: size.WidthMM, HeightMM: size.HeightMM, White: white}
	if err := pdfgen.Build(job, f); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	fmt.Printf("\nwrote %s — %g × %g mm, layers: %s\n", out, size.WidthMM, size.HeightMM, layerSummary(white))
	fmt.Println("check it with Tayda's PDF Analyzer before ordering.")
	return nil
}

func layerSummary(w pdfgen.WhiteMode) string {
	if w == pdfgen.WhiteNone {
		return pdfgen.LayerCMYK
	}
	return pdfgen.SpotWhite + " + " + pdfgen.LayerCMYK
}

func printReport(img *artwork.Image, r artwork.Report) {
	fmt.Printf("%s: %s, %d × %d px\n", img.Path, strings.ToUpper(img.Format), r.PixelW, r.PixelH)
	fmt.Printf("side %s: %s, artwork lands at %.0f × %.0f DPI\n", r.Side, r.Target, r.DPIX, r.DPIY)
	for _, w := range r.Warnings {
		fmt.Printf("  warning: %s\n", w)
	}
	for _, p := range r.Problems {
		fmt.Printf("  problem: %s\n", p)
	}
	if r.OK() && len(r.Warnings) == 0 {
		fmt.Println("  ok")
	}
}
