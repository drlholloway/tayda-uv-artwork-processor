package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/laneholloway/tayda-uv-artwork-processor/internal/enclosure"
)

// command describes a subcommand well enough to print its help, so the
// description of what a command does lives in exactly one place.
type command struct {
	name     string
	synopsis string // the invocation line, minus the program name
	summary  string // one line, shown in the command list
	notes    string // optional detail, printed after the options
	examples []string

	// flagSet builds a set with this command's flags registered against a
	// throwaway options struct, so help can list them without running
	// anything. Nil for commands that take no flags.
	flagSet func() *flag.FlagSet

	// targets marks commands that take -e/-s, which get the list of known
	// enclosures and sides appended to their help.
	targets bool
}

var commands = []command{
	{
		name:     "enclosures",
		synopsis: "enclosures",
		summary:  "List the enclosures with known artboard sizes.",
		examples: []string{"tayda-uv enclosures"},
	},
	{
		name:     "sides",
		synopsis: "sides <enclosure>",
		summary:  "Show each side's artboard size, pixel minimum and tolerance.",
		notes: "Tolerance is Tayda's printing accuracy for that side, not slack in\n" +
			"the artboard size. The artboard itself must match exactly.",
		examples: []string{"tayda-uv sides 1590B"},
	},
	{
		name:     "validate",
		synopsis: "validate -e <enclosure> -s <side> <image>",
		summary:  "Check artwork against a side without writing anything.",
		notes: "Exits 0 if the artwork is ready to print, 1 if it is not, so this is\n" +
			"the command to use in a script.",
		examples: []string{"tayda-uv validate -e 1590B -s A face.png"},
		flagSet:  func() *flag.FlagSet { return validateFlags(&validateOpts{}) },
		targets:  true,
	},
	{
		name:     "convert",
		synopsis: "convert -e <enclosure> -s <side> [options] <image>",
		summary:  "Write a print-ready PDF for one enclosure side.",
		notes: `Run it once per side; each PDF holds a single artboard, which is what
the Tayda Box Tool expects.

White ink (-white). CMYK has no white, so on a dark enclosure your colours
sink into the powder coat without an undercoat beneath them:
  none      printing on a white enclosure, no undercoat needed
  auto      follow the artwork's transparency (default)
  full      flood the whole side with white

Gloss varnish (-gloss). A paid add-on, so it is off unless asked for:
  none      no varnish (default)
  full      varnish the whole side
  artwork   varnish wherever the artwork is opaque
  mask      varnish where -gloss-mask says

A gloss mask is read as coverage: if it has transparency its alpha is used,
otherwise its brightness is — white coats, black leaves bare. It is scaled to
the artboard on its own, so it need not match the artwork's pixel size.

Which finish that varnish is, gloss or matte, is chosen in the Tayda Box Tool
when you upload. It is not recorded in the PDF.`,
		examples: []string{
			"tayda-uv convert -e 1590B -s A face.png",
			"tayda-uv convert -e 1590B -s A -white full -o face.pdf face.png",
			"tayda-uv convert -e 1590B -s A -gloss-mask logo-only.png face.png",
			"tayda-uv convert -e 1590A -s Lid -white none back.png",
		},
		flagSet: func() *flag.FlagSet { return convertFlags(&convertOpts{}) },
		targets: true,
	},
}

func lookupCommand(name string) (command, bool) {
	for _, c := range commands {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

const reference = `Artboard sizes come from the Tayda UV Printing Service File Preparation Guide:
https://www.taydaelectronics.com/uv-printing-service-guide-v1

Tayda prints files exactly as submitted and does not correct them. Check the
result with their PDF Analyzer and order a single enclosure before a batch.`

// printUsage writes the top-level help.
func printUsage(w io.Writer) {
	fmt.Fprint(w, "tayda-uv — prepare artwork for the Tayda UV printing service\n\n")
	fmt.Fprint(w, "Usage:\n  tayda-uv <command> [options]\n\nCommands:\n")

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range commands {
		fmt.Fprintf(tw, "  %s\t%s\n", c.name, c.summary)
	}
	tw.Flush()

	fmt.Fprint(w, "\nExamples:\n")
	for _, ex := range []string{
		"tayda-uv sides 1590B",
		"tayda-uv validate -e 1590B -s A face.png",
		"tayda-uv convert -e 1590B -s A -white full -o face.pdf face.png",
	} {
		fmt.Fprintf(w, "  %s\n", ex)
	}

	printTargets(w)
	fmt.Fprint(w, "\nRun 'tayda-uv help <command>' for detail on one command.\n")
	fmt.Fprintf(w, "\n%s\n", reference)
}

// printUsage writes the help for one command.
func (c command) printUsage(w io.Writer) {
	fmt.Fprintf(w, "Usage: tayda-uv %s\n\n%s\n", c.synopsis, c.summary)

	if c.flagSet != nil {
		fmt.Fprint(w, "\nOptions:\n")
		printFlags(w, c.flagSet())
	}
	if c.notes != "" {
		fmt.Fprintf(w, "\n%s\n", c.notes)
	}
	if len(c.examples) > 0 {
		fmt.Fprint(w, "\nExamples:\n")
		for _, ex := range c.examples {
			fmt.Fprintf(w, "  %s\n", ex)
		}
	}
	if c.targets {
		printTargets(w)
	}
}

// printFlags lists a command's options in two aligned columns, which the
// flag package's own two-line-per-flag default does not.
func printFlags(w io.Writer, fs *flag.FlagSet) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fs.VisitAll(func(f *flag.Flag) {
		usage := f.Usage
		// Skip the default for booleans and for flags whose usage text
		// already explains what happens when they are left out.
		if f.DefValue != "" && f.DefValue != "false" {
			usage += fmt.Sprintf(" (default %s)", f.DefValue)
		}
		fmt.Fprintf(tw, "  -%s\t%s\n", f.Name, usage)
	})
	tw.Flush()
}

// printTargets lists what -e and -s accept, read from the catalogue so it
// cannot drift from what the tool actually supports.
func printTargets(w io.Writer) {
	sides := make([]string, len(enclosure.Sides))
	for i, s := range enclosure.Sides {
		sides[i] = string(s)
	}
	fmt.Fprintf(w, "\nEnclosures: %s\n", strings.Join(enclosure.Names(), ", "))
	fmt.Fprintf(w, "Sides:      %s\n", strings.Join(sides, ", "))
}

// wantsHelp reports whether the user asked for help rather than for work.
func wantsHelp(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "-h" || a == "-help" || a == "--help" {
			return true
		}
	}
	return false
}
