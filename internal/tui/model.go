// Package tui is the interactive front end: pick an enclosure, attach artwork
// to each side, and convert them all in one pass.
//
// It is a shell over the same packages the CLI uses and holds no printing
// knowledge of its own. Anything it can do must remain doable from the
// command line, since automating this tool is the point of it existing.
package tui

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/bubbles/filepicker"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/laneholloway/tayda-uv-artwork-processor/internal/artwork"
	"github.com/laneholloway/tayda-uv-artwork-processor/internal/enclosure"
	"github.com/laneholloway/tayda-uv-artwork-processor/internal/pdfgen"
)

type screen int

const (
	screenEnclosure screen = iota
	screenSides
	screenPicker
	screenResults
)

// picking says what the file picker was opened for.
type picking int

const (
	pickArtwork picking = iota
	pickMask
)

// sideState is the artwork attached to one side, with its validation already
// done so the table can show status without re-checking on every keystroke.
type sideState struct {
	artPath  string
	art      *artwork.Image
	artRep   artwork.Report
	maskPath string
	mask     *artwork.Image
	maskRep  artwork.Report
}

// gloss reports the mode this side converts with: attaching a mask to a side
// overrides whatever the global gloss setting is.
func (s *sideState) gloss(global pdfgen.GlossMode) pdfgen.GlossMode {
	if s != nil && s.mask != nil {
		return pdfgen.GlossMask
	}
	return global
}

// result is one converted side.
type result struct {
	side   enclosure.Side
	path   string
	layers string
	err    error
}

type convertedMsg struct{ results []result }

// Model is the whole interface state.
type Model struct {
	screen screen

	names     []string // enclosure names, for the first screen
	encCursor int
	enc       enclosure.Enclosure
	chosen    bool

	sideCursor int
	sides      map[enclosure.Side]*sideState

	white pdfgen.WhiteMode
	gloss pdfgen.GlossMode

	picker   filepicker.Model
	pickFor  enclosure.Side
	pickWhat picking

	outDir  string
	results []result
	status  string // transient one-line message

	width, height int
}

// New builds the starting state.
func New() (Model, error) {
	wd, err := os.Getwd()
	if err != nil {
		return Model{}, err
	}

	fp := filepicker.New()
	fp.AllowedTypes = []string{".png", ".jpg", ".jpeg", ".gif"}
	fp.CurrentDirectory = wd
	fp.DirAllowed = false
	fp.FileAllowed = true
	fp.AutoHeight = false
	fp.SetHeight(12)

	return Model{
		screen: screenEnclosure,
		names:  enclosure.Names(),
		sides:  map[enclosure.Side]*sideState{},
		white:  pdfgen.WhiteAuto,
		gloss:  pdfgen.GlossNone,
		picker: fp,
		outDir: wd,
	}, nil
}

// Run starts the interactive interface.
func Run() error {
	m, err := New()
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// Leave room for the header and the key hints around the picker.
		if h := msg.Height - 10; h > 3 {
			m.picker.SetHeight(h)
		}
		return m, nil

	case convertedMsg:
		m.results = msg.results
		m.screen = screenResults
		return m, nil

	case tea.KeyMsg:
		// Ctrl+C always quits, whatever screen we are on.
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		switch m.screen {
		case screenEnclosure:
			return m.updateEnclosure(msg)
		case screenSides:
			return m.updateSides(msg)
		case screenPicker:
			return m.updatePicker(msg)
		case screenResults:
			return m.updateResults(msg)
		}
	}

	if m.screen == screenPicker {
		return m.updatePicker(msg)
	}
	return m, nil
}

func (m Model) updateEnclosure(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		return m, tea.Quit
	case "up", "k":
		if m.encCursor > 0 {
			m.encCursor--
		}
	case "down", "j":
		if m.encCursor < len(m.names)-1 {
			m.encCursor++
		}
	case "home", "g":
		m.encCursor = 0
	case "end", "G":
		m.encCursor = len(m.names) - 1
	case "enter", " ":
		e, err := enclosure.Lookup(m.names[m.encCursor])
		if err != nil {
			m.status = err.Error()
			return m, nil
		}
		// Changing enclosure invalidates artwork chosen for the old one,
		// since every side is a different size.
		if m.chosen && e.Name != m.enc.Name {
			m.sides = map[enclosure.Side]*sideState{}
		}
		m.enc, m.chosen = e, true
		m.screen = screenSides
		m.status = ""
	}
	return m, nil
}

func (m Model) updateSides(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	side := enclosure.Sides[m.sideCursor]

	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "esc", "e":
		m.screen = screenEnclosure
		m.status = ""
	case "up", "k":
		if m.sideCursor > 0 {
			m.sideCursor--
		}
	case "down", "j":
		if m.sideCursor < len(enclosure.Sides)-1 {
			m.sideCursor++
		}
	case "enter":
		return m.openPicker(side, pickArtwork)
	case "m":
		if m.sides[side] == nil || m.sides[side].art == nil {
			m.status = "set the artwork for this side first"
			return m, nil
		}
		return m.openPicker(side, pickMask)
	case "x", "delete", "backspace":
		if m.sides[side] != nil {
			delete(m.sides, side)
			m.status = fmt.Sprintf("cleared side %s", side)
		}
	case "w":
		m.white = cycleWhite(m.white)
		m.status = ""
	case "g":
		m.gloss = cycleGloss(m.gloss)
		m.status = ""
	case "c":
		if !m.anyArtwork() {
			m.status = "nothing to convert — press enter to attach artwork to a side"
			return m, nil
		}
		if bad := m.blocking(); bad != "" {
			m.status = bad
			return m, nil
		}
		return m, m.convert()
	}
	return m, nil
}

func (m Model) openPicker(side enclosure.Side, what picking) (tea.Model, tea.Cmd) {
	m.pickFor, m.pickWhat = side, what
	m.screen = screenPicker
	m.status = ""
	return m, m.picker.Init()
}

func (m Model) updatePicker(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc", "q":
			m.screen = screenSides
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.picker, cmd = m.picker.Update(msg)

	if ok, path := m.picker.DidSelectFile(msg); ok {
		m.attach(path)
		m.screen = screenSides
		return m, nil
	}
	if ok, path := m.picker.DidSelectDisabledFile(msg); ok {
		m.status = fmt.Sprintf("%s is not an image this tool reads", filepath.Base(path))
		return m, cmd
	}
	return m, cmd
}

// attach loads a chosen file and validates it against the side it is for, so
// the table can show the verdict immediately.
func (m *Model) attach(path string) {
	size, err := m.enc.Size(m.pickFor)
	if err != nil {
		m.status = err.Error()
		return
	}
	img, err := artwork.Load(path)
	if err != nil {
		m.status = err.Error()
		return
	}

	st := m.sides[m.pickFor]
	if st == nil {
		st = &sideState{}
		m.sides[m.pickFor] = st
	}

	switch m.pickWhat {
	case pickArtwork:
		st.artPath, st.art = path, img
		st.artRep = artwork.Check(img, m.pickFor, size)
	case pickMask:
		st.maskPath, st.mask = path, img
		st.maskRep = artwork.CheckMask(img, m.pickFor, size)
	}
	m.status = ""
}

func (m Model) updateResults(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "q" {
		return m, tea.Quit
	}
	m.screen = screenSides
	return m, nil
}

func (m Model) anyArtwork() bool {
	for _, st := range m.sides {
		if st.art != nil {
			return true
		}
	}
	return false
}

// blocking returns a message naming the first side that would produce a PDF
// Tayda cannot print, or "" if every attached side is good. The TUI has no
// -force: the CLI is the place to override a refusal deliberately.
func (m Model) blocking() string {
	for _, side := range enclosure.Sides {
		st := m.sides[side]
		if st == nil || st.art == nil {
			continue
		}
		if !st.artRep.OK() {
			return fmt.Sprintf("side %s: %s", side, st.artRep.Problems[0])
		}
		if st.mask != nil && !st.maskRep.OK() {
			return fmt.Sprintf("side %s gloss mask: %s", side, st.maskRep.Problems[0])
		}
	}
	return ""
}

// convert writes every attached side. It runs as a command rather than inline
// so a slow disk cannot freeze the interface mid-keystroke.
func (m Model) convert() tea.Cmd {
	type job struct {
		side  enclosure.Side
		state sideState
	}

	// Snapshot everything the write needs; the model must not be read from
	// inside the command.
	enc, white, gloss, outDir := m.enc, m.white, m.gloss, m.outDir
	var jobs []job
	for _, side := range enclosure.Sides {
		if st := m.sides[side]; st != nil && st.art != nil {
			jobs = append(jobs, job{side, *st})
		}
	}

	return func() tea.Msg {
		out := make([]result, 0, len(jobs))
		for _, j := range jobs {
			g := j.state.gloss(gloss)
			r := result{
				side:   j.side,
				path:   filepath.Join(outDir, fmt.Sprintf("%s-%s.pdf", enc.Name, j.side)),
				layers: pdfgen.LayerSummary(white, g),
			}

			size, err := enc.Size(j.side)
			if err != nil {
				r.err = err
				out = append(out, r)
				continue
			}
			f, err := os.Create(r.path)
			if err != nil {
				r.err = err
				out = append(out, r)
				continue
			}

			build := pdfgen.Job{
				Image: j.state.art, WidthMM: size.WidthMM, HeightMM: size.HeightMM,
				White: white, Gloss: g,
			}
			if j.state.mask != nil {
				build.GlossMask = j.state.mask
			}
			r.err = pdfgen.Build(build, f)
			if cerr := f.Close(); cerr != nil && r.err == nil {
				r.err = cerr
			}
			out = append(out, r)
		}
		return convertedMsg{out}
	}
}

func cycleWhite(w pdfgen.WhiteMode) pdfgen.WhiteMode {
	switch w {
	case pdfgen.WhiteNone:
		return pdfgen.WhiteAuto
	case pdfgen.WhiteAuto:
		return pdfgen.WhiteFull
	default:
		return pdfgen.WhiteNone
	}
}

// cycleGloss skips GlossMask: a mask is per-side, attached with "m", not a
// global setting.
func cycleGloss(g pdfgen.GlossMode) pdfgen.GlossMode {
	switch g {
	case pdfgen.GlossNone:
		return pdfgen.GlossFull
	case pdfgen.GlossFull:
		return pdfgen.GlossArtwork
	default:
		return pdfgen.GlossNone
	}
}
