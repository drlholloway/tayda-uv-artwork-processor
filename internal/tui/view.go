package tui

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/laneholloway/tayda-uv-artwork-processor/internal/enclosure"
	"github.com/laneholloway/tayda-uv-artwork-processor/internal/pdfgen"
)

// Colours are adaptive so the interface stays legible on a light terminal as
// well as a dark one.
var (
	titleStyle  = lipgloss.NewStyle().Bold(true)
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "246", Dark: "243"})
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "240", Dark: "247"})
	cursorStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "27", Dark: "81"})
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "28", Dark: "42"})
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "130", Dark: "214"})
	badStyle    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "160", Dark: "203"})
	keyStyle    = lipgloss.NewStyle().Bold(true)
)

func (m Model) View() string {
	switch m.screen {
	case screenEnclosure:
		return m.viewEnclosure()
	case screenSides:
		return m.viewSides()
	case screenPicker:
		return m.viewPicker()
	case screenResults:
		return m.viewResults()
	}
	return ""
}

func (m Model) viewEnclosure() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("tayda-uv") + dimStyle.Render(" — prepare artwork for Tayda UV printing") + "\n\n")
	b.WriteString(headerStyle.Render("  Choose an enclosure") + "\n\n")

	for i, name := range m.names {
		face := ""
		if e, err := enclosure.Lookup(name); err == nil {
			if sz, err := e.Size(enclosure.SideA); err == nil {
				face = fmt.Sprintf("face %g × %g mm", sz.WidthMM, sz.HeightMM)
			}
		}
		row := fmt.Sprintf("%s %s", pad(name, 9), dimStyle.Render(face))
		if i == m.encCursor {
			b.WriteString(cursorStyle.Render("▸ ") + cursorStyle.Render(pad(name, 9)) + " " + dimStyle.Render(face))
		} else {
			b.WriteString("  " + row)
		}
		b.WriteString("\n")
	}

	b.WriteString("\n" + m.statusLine())
	b.WriteString(hints(
		"↑↓", "move",
		"enter", "select",
		"q", "quit",
	))
	return b.String()
}

func (m Model) viewSides() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("tayda-uv") + dimStyle.Render(" · ") + titleStyle.Render(m.enc.Name) + "\n\n")

	b.WriteString(headerStyle.Render(
		"  " + pad("SIDE", 6) + pad("ARTBOARD", 16) + pad("ARTWORK", 22) + pad("GLOSS", 9) + "STATUS"))
	b.WriteString("\n")

	for i, side := range enclosure.Sides {
		sz, err := m.enc.Size(side)
		if err != nil {
			continue
		}
		st := m.sides[side]

		art := dimStyle.Render(pad("—", 22))
		if st != nil && st.art != nil {
			art = pad(truncate(filepath.Base(st.artPath), 21), 22)
		}

		gloss := dimStyle.Render(pad("—", 9))
		if st != nil && st.art != nil {
			if g := st.gloss(m.gloss); g != pdfgen.GlossNone {
				gloss = pad(g.String(), 9)
			}
		}

		row := pad(string(side), 6) +
			pad(fmt.Sprintf("%g × %g mm", sz.WidthMM, sz.HeightMM), 16) +
			art + gloss + status(st)

		if i == m.sideCursor {
			b.WriteString(cursorStyle.Render("▸ ") + row)
		} else {
			b.WriteString("  " + row)
		}
		b.WriteString("\n")
	}

	b.WriteString("\n" + m.settingsLine() + "\n")
	b.WriteString(m.detail())
	b.WriteString("\n" + m.statusLine())
	b.WriteString(hints(
		"↑↓", "move",
		"enter", "artwork",
		"m", "gloss mask",
		"x", "clear",
		"w", "white",
		"g", "gloss",
		"c", "convert",
		"e", "enclosure",
		"q", "quit",
	))
	return b.String()
}

func (m Model) viewPicker() string {
	what := "artwork"
	if m.pickWhat == pickMask {
		what = "gloss mask"
	}
	sz, _ := m.enc.Size(m.pickFor)

	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("Choose %s for side %s", what, m.pickFor)) + "\n")
	b.WriteString(dimStyle.Render(fmt.Sprintf("%s %s · artboard %g × %g mm",
		m.enc.Name, m.pickFor, sz.WidthMM, sz.HeightMM)) + "\n\n")
	b.WriteString(m.picker.View() + "\n")
	b.WriteString(m.statusLine())
	b.WriteString(hints("↑↓", "move", "enter", "open or choose", "esc", "cancel"))
	return b.String()
}

func (m Model) viewResults() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Converted") + "\n\n")

	var failed int
	for _, r := range m.results {
		if r.err != nil {
			failed++
			b.WriteString("  " + badStyle.Render("✗ "+pad(filepath.Base(r.path), 24)) +
				badStyle.Render(r.err.Error()) + "\n")
			continue
		}
		b.WriteString("  " + okStyle.Render("✓ ") + pad(filepath.Base(r.path), 24) +
			dimStyle.Render(r.layers) + "\n")
	}

	b.WriteString("\n" + dimStyle.Render("  written to "+m.outDir) + "\n")
	if failed == 0 {
		b.WriteString("\n  " + dimStyle.Render(
			"Check these with Tayda's PDF Analyzer, and order one enclosure before a batch.") + "\n")
	}
	b.WriteString("\n" + m.statusLine())
	b.WriteString(hints("any key", "back", "q", "quit"))
	return b.String()
}

// settingsLine shows the ink options, which apply to every side.
func (m Model) settingsLine() string {
	return "  " + dimStyle.Render("white ") + m.white.String() +
		dimStyle.Render("  ·  gloss ") + m.gloss.String() +
		dimStyle.Render("  ·  out ") + shortenPath(m.outDir)
}

// detail explains the highlighted side in full: the table only has room for a
// symbol, and a problem is worth reading in whole before printing anything.
func (m Model) detail() string {
	side := enclosure.Sides[m.sideCursor]
	st := m.sides[side]
	if st == nil || st.art == nil {
		return "\n  " + dimStyle.Render("no artwork on side "+string(side)+" — press enter to choose a file") + "\n"
	}

	var b strings.Builder
	b.WriteString("\n  " + dimStyle.Render(shortenPath(st.artPath)) + "\n")
	for _, w := range st.artRep.Warnings {
		b.WriteString("  " + warnStyle.Render("⚠ "+w) + "\n")
	}
	for _, p := range st.artRep.Problems {
		b.WriteString("  " + badStyle.Render("✗ "+p) + "\n")
	}
	if st.mask != nil {
		b.WriteString("  " + dimStyle.Render("gloss mask "+shortenPath(st.maskPath)) + "\n")
		for _, w := range st.maskRep.Warnings {
			b.WriteString("  " + warnStyle.Render("⚠ "+w) + "\n")
		}
		for _, p := range st.maskRep.Problems {
			b.WriteString("  " + badStyle.Render("✗ "+p) + "\n")
		}
	}
	return b.String()
}

func (m Model) statusLine() string {
	if m.status == "" {
		return ""
	}
	return "  " + warnStyle.Render(m.status) + "\n"
}

// status is the one-glance verdict for a side.
func status(st *sideState) string {
	if st == nil || st.art == nil {
		return dimStyle.Render("· not set")
	}
	dpi := math.Min(st.artRep.DPIX, st.artRep.DPIY)
	switch {
	case !st.artRep.OK() || (st.mask != nil && !st.maskRep.OK()):
		return badStyle.Render(fmt.Sprintf("✗ %.0f DPI", dpi))
	case len(st.artRep.Warnings) > 0 || (st.mask != nil && len(st.maskRep.Warnings) > 0):
		return warnStyle.Render(fmt.Sprintf("⚠ %.0f DPI", dpi))
	}
	return okStyle.Render(fmt.Sprintf("✓ %.0f DPI", dpi))
}

// hints renders the key legend from alternating key/description pairs.
func hints(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, keyStyle.Render(pairs[i])+" "+dimStyle.Render(pairs[i+1]))
	}
	return "\n  " + strings.Join(parts, dimStyle.Render(" · ")) + "\n"
}

// pad right-pads to a column width measured in display cells, so the table
// does not skew on a filename containing wide characters.
func pad(s string, width int) string {
	if w := lipgloss.Width(s); w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}

func truncate(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	r := []rune(s)
	if width <= 1 || len(r) <= 1 {
		return string(r[:1])
	}
	return string(r[:width-1]) + "…"
}

// shortenPath replaces the home directory with ~ so the settings line stays
// readable in a deep tree.
func shortenPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !strings.HasPrefix(p, home) {
		return p
	}
	return "~" + strings.TrimPrefix(p, home)
}
