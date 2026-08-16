# Tayda UV Artwork Processor
### From Image File to Prepared PDF for Tayda UV Printing Service

## Introduction

The Tayda UV Printing process is a God-send for small pedal builders wanting to create professional looking pedals on a budget. They can go from nothing to a fully professionally finished enclosure in short time and with little money invested. One of the most difficult parts of the process however is to prepare the artwork for printing.

There exists a tool already that handles the front face creation of the needed pdf here: https://forge.optictonelab.com/. However, there is always a chance that it can disappear or not be updated. This is a TUI program that you select the enclosure type, the images for each side and then run the conversion for each side. It can also be run fully from the command line so it can be automated via an agent skill.

The tool verifies the sizes of the image is correct for the enclosure and ensures that the output is correct for tayda.

## Install

```sh
go build -o tayda-uv ./cmd/tayda-uv
```

## Interactive use

Run it with no arguments:

```sh
tayda-uv
```

Choose an enclosure, then attach artwork to each side you want printed and
convert them all in one pass. Every side is validated as you attach it, so
problems show up before you spend anything.

```
tayda-uv · 1590B

  SIDE  ARTBOARD        ARTWORK               GLOSS    STATUS
▸ A     56 × 108.5 mm   face.png              —        ✓ 305 DPI
  B     52 × 24 mm      —                     —        · not set
  C     24 × 103 mm     side-left.png         —        ✗ 212 DPI
  D     52 × 24 mm      —                     —        · not set
  E     24 × 103 mm     —                     —        · not set
  Lid   56 × 108.5 mm   back.png              mask     ✓ 305 DPI

  white auto  ·  gloss none  ·  out ~/pedals/fuzz

  ↑↓ move · enter artwork · m gloss mask · x clear · w white · g gloss
  c convert · e enclosure · q quit
```

Files are written to the current directory as `<enclosure>-<side>.pdf`.

## Command line

Everything the interface does is also a command, so builds can be scripted or
driven by an agent.

```sh
# every command explains itself
tayda-uv help
tayda-uv help convert

# what can it print on?
tayda-uv enclosures

# what size does each side need to be?
tayda-uv sides 1590B
#   SIDE  ARTBOARD       MIN PIXELS @300DPI  TOLERANCE
#   A     56 × 108.5 mm  662 × 1282          ±0.50 mm
#   B     52 × 24 mm     615 × 284           ±1.00 mm
#   ...

# check artwork without writing anything
tayda-uv validate -e 1590B -s A face.png

# write the print-ready PDF
tayda-uv convert -e 1590B -s A -o face.pdf face.png

# read the finished PDF back and check what is actually in it
tayda-uv inspect -e 1590B -s A face.pdf
```

Run `convert` once per side. Each PDF is a single artboard, which is what the
Tayda Box Tool expects.

### White ink

Tayda prints CMYK, which has no white. On a dark enclosure your colours sink
into the powder coat unless an `RDG_WHITE` undercoat goes down first.
`-white` controls it:

| Mode | Use it when |
|---|---|
| `auto` (default) | Artwork has transparency. Opaque pixels get white, transparent ones stay bare enclosure. |
| `full` | You want the whole side backed in white. |
| `none` | Printing on a white enclosure. |

### Gloss / varnish (paid add-on)

`RDG_GLOSS` adds a varnish coat over the print. It is off unless you ask for
it. `-gloss` takes:

| Mode | Effect |
|---|---|
| `none` (default) | No varnish layer. |
| `full` | Varnish the whole side. |
| `artwork` | Varnish wherever the artwork is opaque. |
| `mask` | Varnish according to `-gloss-mask` — this is how you coat one element. |

A mask is read as coverage: if it has transparency, its alpha is used, so a
shape on a transparent background works directly. Otherwise brightness is
used — **white coats, black leaves bare**. The mask does not have to match the
artwork's pixel dimensions; it is scaled to the artboard independently.

```sh
# varnish only the logo
tayda-uv convert -e 1590B -s A -gloss-mask logo-only.png -o face.pdf face.png
```

Two things the guide is firm about, which the tool repeats back to you:

- Large areas of **gloss varnish** attract fingerprints and add days to
  production. Gloss matte handles large areas better. The tool prints the
  coverage percentage and warns above 50%. That percentage is measured from
  the varnish layer it actually wrote, so `artwork` reports how much of the
  side your artwork really covers rather than assuming all of it.
- Whether the finish is gloss varnish or gloss matte is **not** in the PDF —
  you choose it in the Tayda Box Tool when saving the template.

### Artwork formats

PNG, JPEG, GIF and SVG.

An SVG has no pixel size of its own, so it is rendered for the side it is
going on — at 600 DPI unless you pass `-dpi` — and everything after that
treats it as an ordinary image. The PDF holds pixels, not paths.

```sh
tayda-uv convert -e 1590B -s A face.svg
```

Rendering keeps the SVG's own proportions, so artwork of the wrong shape is
still reported as the wrong shape rather than quietly stretched to fit.

Two things to know before exporting:

- **Convert text to paths.** Text is not rendered. In Inkscape that is
  *Path > Object to Path*; in Illustrator, *Type > Create Outlines*.
- **Flatten clips, masks, filters, patterns and embedded images.** These are
  not rendered either.

Neither is silently dropped — an SVG using them is refused, with a message
saying which. That is deliberate: Tayda prints the file exactly as submitted,
so artwork that lost its lettering somewhere in this tool would not be caught
by anything downstream. If flattening is awkward, export a PNG at the size
`tayda-uv sides <enclosure>` lists.

### Validation

`convert` validates before writing and refuses to produce a file that will not
print correctly. It checks that the artwork resolves to at least 300 DPI at
physical size, that its aspect ratio matches the artboard, and warns when an
image looks rotated 90°. Use `-force` to write anyway.

`validate` exits 0 when the artwork is ready and 1 when it is not, so it works
in a script. Exit 2 always means the command itself was typed wrong.

### Inspection

`validate` looks at artwork going in. `inspect` looks at the PDF coming out:

```sh
tayda-uv inspect face.pdf
#   face.pdf: 1 page, 158.74 × 307.56 pt
#     artboard: 56 × 108.5 mm            1590B side A, 1590B side Lid
#     colour:   DeviceCMYK               no RGB OK
#     spots:    RDG_WHITE                OK
#     order:    RDG_WHITE → CMYK         OK
```

It re-reads the finished file the way an unfamiliar consumer would, rather than
trusting the code that wrote it, and checks the things the guide is strict
about: one artboard per file, at a size that is really an enclosure side; no RGB
anywhere; spot names spelled exactly `RDG_WHITE` and `RDG_GLOSS`, which are
case-sensitive; and White → CMYK → Gloss in that order.

Add `-e` and `-s` to assert a specific side, which is what you want after a
scripted `convert`. Without them it names every side matching that size — often
two, since A and the Lid are identical, as are B and D, and C and E. It exits 0
when every file passes and 1 when any does not.

### None of this is Tayda's verdict

`inspect` checks a file's structure. It cannot tell you the artwork is the right
way up, or that the colours will come out as you pictured them. Tayda's own
[PDF Analyzer](https://pdf.tayda.com) (user `pdfman` / pass `pdfman`) is the
authority on whether they will print something — upload the file there before
ordering, and always order a small quantity first.

That step is deliberately manual. The Analyzer's API sits behind a login wrapped
in reCAPTCHA, and scripting past bot detection someone deployed on purpose is
not a thing this tool does.

## Agent skill

`.claude/skills/tayda-uv/` is a [Claude Code](https://claude.com/claude-code)
skill that teaches an agent to drive this tool: the validate-then-convert
workflow, how to pick `-white` from the enclosure's colour, that gloss is a paid
add-on and stays off unless asked, and that `-force` is never its call to make.

It is active for any agent working inside this repo. To use it anywhere — which
is the point, since your artwork lives elsewhere:

```sh
go install ./cmd/tayda-uv          # puts tayda-uv on PATH via ~/go/bin
ln -s "$PWD/.claude/skills/tayda-uv" ~/.claude/skills/tayda-uv
```

## References
[Tayda UV Printing Service Guide](https://www.taydaelectronics.com/uv-printing-service-guide-v1)
— currently serving V2 (April 22 2026) despite the `v1` URL. The artboard
dimensions in `internal/enclosure` are transcribed from its table.

