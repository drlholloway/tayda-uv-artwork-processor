// Command tayda-uv prepares artwork for the Tayda UV printing service.
//
// Every operation is reachable from the command line so the tool can be
// driven by a script or an agent, not only by a human at a terminal.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/laneholloway/tayda-uv-artwork-processor/internal/artwork"
	"github.com/laneholloway/tayda-uv-artwork-processor/internal/enclosure"
	"github.com/laneholloway/tayda-uv-artwork-processor/internal/pdfgen"
	"github.com/laneholloway/tayda-uv-artwork-processor/internal/tui"
)

// usageError is a mistake in how the command was invoked, as opposed to a
// problem with the artwork. It exits 2 and points at the relevant help.
type usageError struct {
	cmd string
	msg string
}

func (e *usageError) Error() string { return e.msg }

// isTerminal reports whether f is an interactive terminal rather than a pipe
// or a file.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// errParsed stands in for a flag-parsing failure the flag package has already
// reported, so main exits without printing a second message.
var errParsed = errors.New("flag parse error")

func main() {
	if len(os.Args) < 2 {
		// Bare invocation opens the interactive interface, but only when
		// there is a terminal to draw on. Piped or redirected, printing the
		// usage is far more useful than failing to start a TUI.
		if isTerminal(os.Stdin) && isTerminal(os.Stdout) {
			if err := tui.Run(); err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
			return
		}
		printUsage(os.Stderr)
		os.Exit(2)
	}

	name, args := os.Args[1], os.Args[2:]

	// Asking for help is not an error, so it goes to stdout and exits 0 —
	// `tayda-uv --help | less` should show something.
	if name == "help" || name == "-h" || name == "-help" || name == "--help" {
		os.Exit(runHelp(args))
	}

	var err error
	switch name {
	case "enclosures":
		err = cmdEnclosures(args)
	case "sides":
		err = cmdSides(args)
	case "validate":
		err = cmdValidate(args)
	case "convert":
		err = cmdConvert(args)
	default:
		fmt.Fprintf(os.Stderr, "error: unknown command %q\n\n", name)
		printUsage(os.Stderr)
		os.Exit(2)
	}

	if err != nil {
		os.Exit(report(os.Stderr, err))
	}
}

// report prints an error in the shape its kind deserves and returns the exit
// status to use: 2 when the command was typed wrong, 1 when the work itself
// failed. Scripts can tell the two apart.
func report(w io.Writer, err error) int {
	if errors.Is(err, errParsed) {
		return 2 // the flag package already said what was wrong
	}
	var ue *usageError
	if errors.As(err, &ue) {
		fmt.Fprintf(w, "error: %s\n", ue.msg)
		if ue.cmd != "" {
			fmt.Fprintf(w, "run 'tayda-uv help %s' for usage.\n", ue.cmd)
		}
		return 2
	}
	fmt.Fprintln(w, "error:", err)
	return 1
}

func runHelp(args []string) int {
	if len(args) == 0 {
		printUsage(os.Stdout)
		return 0
	}
	c, ok := lookupCommand(args[0])
	if !ok {
		fmt.Fprintf(os.Stderr, "error: unknown command %q\n\n", args[0])
		printUsage(os.Stderr)
		return 2
	}
	c.printUsage(os.Stdout)
	return 0
}

// setup wires a command's flag set to its own help text, so a bad flag prints
// something useful, and handles an explicit -h before any parsing.
func setup(name string, fs *flag.FlagSet, args []string) (helped bool, err error) {
	c, ok := lookupCommand(name)
	if !ok {
		return false, fmt.Errorf("internal error: no help registered for %q", name)
	}
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { c.printUsage(os.Stderr) }

	if wantsHelp(args) {
		c.printUsage(os.Stdout)
		return true, nil
	}
	if err := fs.Parse(args); err != nil {
		return false, errParsed
	}
	return false, nil
}

func cmdEnclosures(args []string) error {
	if wantsHelp(args) {
		c, _ := lookupCommand("enclosures")
		c.printUsage(os.Stdout)
		return nil
	}
	if len(args) != 0 {
		return &usageError{cmd: "enclosures", msg: "enclosures takes no arguments"}
	}
	for _, name := range enclosure.Names() {
		fmt.Println(name)
	}
	return nil
}

func cmdSides(args []string) error {
	if wantsHelp(args) {
		c, _ := lookupCommand("sides")
		c.printUsage(os.Stdout)
		return nil
	}
	if len(args) != 1 {
		return &usageError{cmd: "sides", msg: "sides takes exactly one enclosure name"}
	}
	e, err := enclosure.Lookup(args[0])
	if err != nil {
		// Naming an enclosure that does not exist is a typing mistake, the
		// same as it is for convert's -e.
		return &usageError{cmd: "sides", msg: err.Error()}
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

type validateOpts struct {
	enclosure string
	side      string
}

func validateFlags(o *validateOpts) *flag.FlagSet {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.StringVar(&o.enclosure, "e", "", "enclosure name, e.g. 1590B")
	fs.StringVar(&o.side, "s", "", "side to print on")
	return fs
}

func cmdValidate(args []string) error {
	var o validateOpts
	fs := validateFlags(&o)
	helped, err := setup("validate", fs, args)
	if helped || err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return &usageError{cmd: "validate", msg: "validate needs exactly one image"}
	}

	size, side, err := resolve("validate", o.enclosure, o.side)
	if err != nil {
		return err
	}
	img, err := artwork.Load(fs.Arg(0))
	if err != nil {
		return err
	}

	report := artwork.Check(img, side, size)
	printReport("artwork", img, report)
	if !report.OK() {
		return fmt.Errorf("artwork is not ready for printing")
	}
	return nil
}

type convertOpts struct {
	enclosure string
	side      string
	white     string
	gloss     string
	glossMask string
	out       string
	force     bool
}

func convertFlags(o *convertOpts) *flag.FlagSet {
	fs := flag.NewFlagSet("convert", flag.ContinueOnError)
	fs.StringVar(&o.enclosure, "e", "", "enclosure name, e.g. 1590B")
	fs.StringVar(&o.side, "s", "", "side to print on")
	fs.StringVar(&o.white, "white", "auto", "RDG_WHITE undercoat: none, auto or full")
	fs.StringVar(&o.gloss, "gloss", "none", "RDG_GLOSS varnish: none, full, artwork or mask")
	fs.StringVar(&o.glossMask, "gloss-mask", "", "image marking where varnish goes; implies -gloss mask")
	fs.StringVar(&o.out, "o", "", "output PDF, default <image>-<enclosure>-<side>.pdf")
	fs.BoolVar(&o.force, "force", false, "write the PDF even if validation finds problems")
	return fs
}

func cmdConvert(args []string) error {
	var o convertOpts
	fs := convertFlags(&o)
	helped, err := setup("convert", fs, args)
	if helped || err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return &usageError{cmd: "convert", msg: "convert needs exactly one image"}
	}

	size, side, err := resolve("convert", o.enclosure, o.side)
	if err != nil {
		return err
	}
	white, err := pdfgen.ParseWhiteMode(o.white)
	if err != nil {
		return &usageError{cmd: "convert", msg: err.Error()}
	}
	gloss, err := glossMode(o.gloss, o.glossMask)
	if err != nil {
		return &usageError{cmd: "convert", msg: err.Error()}
	}

	in := fs.Arg(0)
	img, err := artwork.Load(in)
	if err != nil {
		return err
	}

	rep := artwork.Check(img, side, size)
	printReport("artwork", img, rep)
	ok := rep.OK()

	var mask *artwork.Image
	if gloss == pdfgen.GlossMask {
		if mask, err = artwork.Load(o.glossMask); err != nil {
			return err
		}
		maskReport := artwork.CheckMask(mask, side, size)
		printReport("gloss mask", mask, maskReport)
		ok = ok && maskReport.OK()
	}
	if !ok && !o.force {
		return fmt.Errorf("refusing to write a PDF that will not print correctly (use -force to override)")
	}

	out := o.out
	if out == "" {
		base := strings.TrimSuffix(filepath.Base(in), filepath.Ext(in))
		out = fmt.Sprintf("%s-%s-%s.pdf", base, strings.ToLower(o.enclosure), strings.ToLower(string(side)))
	}

	f, err := os.Create(out)
	if err != nil {
		return err
	}
	// Close explicitly so a write error on flush is not swallowed, but keep a
	// deferred close for the error paths.
	defer f.Close()

	job := pdfgen.Job{
		Image: img, WidthMM: size.WidthMM, HeightMM: size.HeightMM,
		White: white, Gloss: gloss,
	}
	if mask != nil {
		job.GlossMask = mask
	}
	if err := pdfgen.Build(job, f); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	fmt.Printf("\nwrote %s — %g × %g mm, layers: %s\n", out, size.WidthMM, size.HeightMM, pdfgen.LayerSummary(white, gloss))
	printGlossNotes(gloss, mask)
	fmt.Println("check it with Tayda's PDF Analyzer before ordering.")
	return nil
}

// resolve turns the -e/-s flags into an artboard size.
func resolve(cmd, encName, sideName string) (enclosure.Size, enclosure.Side, error) {
	switch {
	case encName == "" && sideName == "":
		return enclosure.Size{}, "", &usageError{cmd: cmd, msg: "-e <enclosure> and -s <side> are required"}
	case encName == "":
		return enclosure.Size{}, "", &usageError{cmd: cmd, msg: "-e <enclosure> is required"}
	case sideName == "":
		return enclosure.Size{}, "", &usageError{cmd: cmd, msg: "-s <side> is required"}
	}
	e, err := enclosure.Lookup(encName)
	if err != nil {
		return enclosure.Size{}, "", &usageError{cmd: cmd, msg: err.Error()}
	}
	side, err := enclosure.ParseSide(sideName)
	if err != nil {
		return enclosure.Size{}, "", &usageError{cmd: cmd, msg: err.Error()}
	}
	sz, err := e.Size(side)
	return sz, side, err
}

// glossMode reconciles the two ways of asking for varnish. Supplying a mask
// is enough on its own; contradicting yourself is an error rather than a
// guess, because guessing wrong here costs an enclosure.
func glossMode(name, maskPath string) (pdfgen.GlossMode, error) {
	mode, err := pdfgen.ParseGlossMode(name)
	if err != nil {
		return 0, err
	}
	switch {
	case maskPath != "" && mode == pdfgen.GlossNone:
		return pdfgen.GlossMask, nil
	case maskPath != "" && mode != pdfgen.GlossMask:
		return 0, fmt.Errorf("-gloss-mask conflicts with -gloss %s: drop one", mode)
	case maskPath == "" && mode == pdfgen.GlossMask:
		return 0, fmt.Errorf("-gloss mask needs -gloss-mask <image>")
	}
	return mode, nil
}

// printGlossNotes passes on the guide's warnings about varnish, which are
// about the finished pedal rather than the file.
func printGlossNotes(g pdfgen.GlossMode, mask *artwork.Image) {
	if g == pdfgen.GlossNone {
		return
	}
	coverage := 1.0
	if mask != nil {
		coverage = pdfgen.CoverageFraction(mask)
	}
	fmt.Printf("gloss covers %.0f%% of the side.\n", coverage*100)
	if coverage > 0.5 {
		fmt.Println("  note: the guide warns large gloss varnish areas attract fingerprints and")
		fmt.Println("        add days to production. Gloss matte handles large areas better.")
	}
	fmt.Println("  choose gloss varnish or gloss matte in the Tayda Box Tool — it is not in the file.")
}

func printReport(label string, img *artwork.Image, r artwork.Report) {
	fmt.Printf("%s %s: %s, %d × %d px\n", label, img.Path, strings.ToUpper(img.Format), r.PixelW, r.PixelH)
	fmt.Printf("  side %s: %s, %s lands at %.0f × %.0f DPI\n", r.Side, r.Target, label, r.DPIX, r.DPIY)
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
