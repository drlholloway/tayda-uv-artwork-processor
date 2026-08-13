package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/laneholloway/tayda-uv-artwork-processor/internal/enclosure"
	"github.com/laneholloway/tayda-uv-artwork-processor/internal/pdfgen"
)

// Help is the only documentation a user has at the terminal, so make sure
// every command actually produces some, naming itself and its arguments.
func TestEveryCommandHasUsableHelp(t *testing.T) {
	if len(commands) == 0 {
		t.Fatal("no commands registered")
	}
	for _, c := range commands {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			c.printUsage(&buf)
			out := buf.String()

			if !strings.Contains(out, "tayda-uv "+c.synopsis) {
				t.Errorf("help does not show the invocation line %q", c.synopsis)
			}
			if !strings.Contains(out, c.summary) {
				t.Error("help does not include the summary")
			}
			if len(c.examples) == 0 {
				t.Error("command has no examples")
			}
			for _, ex := range c.examples {
				if !strings.Contains(out, ex) {
					t.Errorf("example missing from help: %s", ex)
				}
			}
			if c.flagSet != nil && !strings.Contains(out, "Options:") {
				t.Error("command has flags but help lists no options")
			}
		})
	}
}

// The commands taking -e/-s should tell the user what those accept, and the
// list has to come from the catalogue rather than a hand-written copy that
// can drift.
func TestTargetCommandsListEnclosuresAndSides(t *testing.T) {
	for _, c := range commands {
		if !c.targets {
			continue
		}
		var buf bytes.Buffer
		c.printUsage(&buf)
		out := buf.String()
		for _, name := range enclosure.Names() {
			if !strings.Contains(out, name) {
				t.Errorf("%s help does not mention enclosure %s", c.name, name)
			}
		}
		for _, s := range enclosure.Sides {
			if !strings.Contains(out, string(s)) {
				t.Errorf("%s help does not mention side %s", c.name, s)
			}
		}
	}
}

func TestTopLevelHelpListsEveryCommand(t *testing.T) {
	var buf bytes.Buffer
	printUsage(&buf)
	out := buf.String()
	for _, c := range commands {
		if !strings.Contains(out, c.name) {
			t.Errorf("top-level help omits %q", c.name)
		}
	}
	// The guide is the authority for everything this tool does; the user
	// should be able to find it from the help.
	if !strings.Contains(out, "taydaelectronics.com") {
		t.Error("top-level help should link the Tayda guide")
	}
}

// Every command named in help must be dispatchable, or the help lies.
func TestHelpCommandsResolve(t *testing.T) {
	for _, c := range commands {
		if _, ok := lookupCommand(c.name); !ok {
			t.Errorf("lookupCommand(%q) failed", c.name)
		}
	}
	if _, ok := lookupCommand("frobnicate"); ok {
		t.Error("lookupCommand should not invent commands")
	}
}

func TestWantsHelp(t *testing.T) {
	yes := [][]string{{"-h"}, {"--help"}, {"-help"}, {"-e", "1590B", "-h"}}
	for _, args := range yes {
		if !wantsHelp(args) {
			t.Errorf("wantsHelp(%v) = false, want true", args)
		}
	}
	no := [][]string{{}, {"-e", "1590B"}, {"face.png"}, {"--", "-h"}}
	for _, args := range no {
		if wantsHelp(args) {
			t.Errorf("wantsHelp(%v) = true, want false", args)
		}
	}
}

// A script needs to tell "you typed it wrong" from "the artwork is bad".
func TestExitStatusSeparatesUsageFromFailure(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"usage error", &usageError{cmd: "convert", msg: "-e is required"}, 2},
		{"flag parse error", errParsed, 2},
		{"work failure", errors.New("artwork is not ready for printing"), 1},
		{"wrapped usage error", fmt.Errorf("outer: %w", &usageError{msg: "bad"}), 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if got := report(&buf, tc.err); got != tc.want {
				t.Errorf("report() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestUsageErrorPointsAtTheRightHelp(t *testing.T) {
	var buf bytes.Buffer
	report(&buf, &usageError{cmd: "convert", msg: "-e <enclosure> is required"})
	out := buf.String()
	if !strings.Contains(out, "-e <enclosure> is required") {
		t.Error("the message should say what was wrong")
	}
	if !strings.Contains(out, "tayda-uv help convert") {
		t.Error("the message should point at the command's help")
	}
}

// Flags are described once, in the same builder help reads and the command
// runs, so the two cannot disagree.
func TestFlagHelpCoversEveryConvertFlag(t *testing.T) {
	c, _ := lookupCommand("convert")
	var buf bytes.Buffer
	c.printUsage(&buf)
	out := buf.String()
	for _, flag := range []string{"-e", "-s", "-white", "-gloss", "-gloss-mask", "-o", "-force"} {
		if !strings.Contains(out, flag) {
			t.Errorf("convert help omits %s", flag)
		}
	}
	// Defaults matter here: gloss costs money and white changes the print.
	if !strings.Contains(out, "default auto") {
		t.Error("help should state that -white defaults to auto")
	}
	if !strings.Contains(out, "default none") {
		t.Error("help should state that -gloss defaults to none")
	}
}

func TestGlossModeReconcilesFlags(t *testing.T) {
	cases := []struct {
		gloss, mask string
		want        pdfgen.GlossMode
		wantErr     bool
	}{
		{"none", "", pdfgen.GlossNone, false},
		{"none", "m.png", pdfgen.GlossMask, false}, // a mask alone is enough
		{"mask", "m.png", pdfgen.GlossMask, false},
		{"full", "", pdfgen.GlossFull, false},
		{"full", "m.png", 0, true}, // contradictory
		{"mask", "", 0, true},      // mask mode with nothing to mask with
	}
	for _, tc := range cases {
		got, err := glossMode(tc.gloss, tc.mask)
		if tc.wantErr {
			if err == nil {
				t.Errorf("glossMode(%q, %q) should fail", tc.gloss, tc.mask)
			}
			continue
		}
		if err != nil {
			t.Errorf("glossMode(%q, %q): %v", tc.gloss, tc.mask, err)
		} else if got != tc.want {
			t.Errorf("glossMode(%q, %q) = %v, want %v", tc.gloss, tc.mask, got, tc.want)
		}
	}
}
