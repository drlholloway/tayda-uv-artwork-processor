# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```sh
go build ./...                      # build
go test ./...                       # all tests
go test ./internal/pdfgen -run TestCrossReferenceTableIsValid -v   # single test
go vet ./... && gofmt -l .          # gofmt -l must print nothing
go build -o tayda-uv ./cmd/tayda-uv # the CLI binary
```

Go is installed via Homebrew at `/opt/homebrew/bin/go`; it may not be on a
non-login shell's PATH.

The tool itself:

```sh
tayda-uv enclosures
tayda-uv sides 1590B
tayda-uv validate -e 1590B -s A face.png
tayda-uv convert  -e 1590B -s A -white full -o face.pdf face.png
```

## The spec is the contract

Everything in this repo exists to satisfy the **Tayda UV Printing Service File
Preparation Guide** (currently V2, April 22 2026):
<https://www.taydaelectronics.com/uv-printing-service-guide-v1>

Two facts from that guide shape every decision here:

1. **Tayda prints the file exactly as submitted.** They do not inspect or
   correct artwork. A wrong artboard size is a ruined enclosure the customer
   pays for. This is why the tool validates before it writes, and why
   `convert` refuses to emit a PDF that fails validation unless `-force`.
2. **The guide is versioned and can change.** The URL still says `v1` while
   serving V2. When touching dimensions or spot-colour rules, re-read the live
   page rather than trusting this repo or your memory. The page rejects
   plain HTTP fetches (403) — read it in a browser.

Tayda's own PDF Analyzer (user `pdfman` / pass `pdfman`) is the final
authority on whether output is acceptable. Recommend it; don't claim a file is
correct without it.

## Architecture

```
cmd/tayda-uv/        CLI (stdlib flag, subcommands)
internal/enclosure/  the artboard dimension table — ground truth
internal/artwork/    decode + validate a source image against a side
internal/pdfgen/     hand-rolled PDF emitter targeting the Tayda spec
```

Data flows one way: `enclosure` supplies a `Size` in millimetres → `artwork`
checks a decoded image against it → `pdfgen` renders the image onto an
artboard of exactly that size. `enclosure` depends on nothing.

**No third-party dependencies, deliberately.** The README's stated motivation
is that the existing web tool may vanish; a build that only needs the Go
toolchain is the answer to that. Do not add a module dependency without a
reason that outweighs it.

### `internal/enclosure`

The table transcribed from the guide: 8 enclosures × 6 sides (A–E plus Lid),
in mm, width × height, in the guide's stated orientation. Invariants the tests
enforce: side A equals the Lid, B equals D, C equals E, on every enclosure.

`ToleranceMM` is Tayda's *printing* tolerance (±0.50 mm on side A, ±1.00 mm
elsewhere) — their registration accuracy, not slack for a mis-sized artboard.

### `internal/artwork`

Checks a decoded image against a side and returns a `Report` splitting
`Problems` (do not print this) from `Warnings` (tell the user, proceed).

It deliberately does **not** demand a specific pixel count. The artboard is
physical; any pixel count that clears 300 DPI at the right aspect ratio is
fine. Checked: effective DPI at final size ≥ 300, aspect drift ≤ 0.5%, and a
portrait/landscape mismatch hint for artwork rotated 90°.

### `internal/pdfgen`

Writes PDF objects directly (`writer.go` handles objects, deflate streams,
xref, trailer; `tayda.go` builds the document). This is hand-rolled because
the spec needs an unusual combination — DeviceCMYK image data, `Separation`
colour spaces with exact spot names, and optional content groups named
`CMYK` / `RDG_WHITE` — that general-purpose Go PDF libraries don't cover
together.

Spec rules encoded here, each load-bearing:

- Page box is exactly the artboard size in mm converted to points.
- Artwork is converted to `DeviceCMYK`. **No RGB may reach the file** — a test
  asserts `/DeviceRGB` is absent.
- Spot names are `RDG_WHITE` / `RDG_GLOSS`, case-sensitive. The guide is
  explicit that `Rdg_White` will not work.
- White is painted **before** CMYK in the content stream, matching the default
  White → CMYK → Gloss print order.
- Nothing is drawn outside the page box.

`WhiteMode` picks the undercoat: `none` (white enclosures), `full` (flood the
artboard), `auto` (follow the artwork's alpha — a `Separation` image whose
sample values are the alpha channel, so transparent areas get no white and the
bare enclosure shows through). Auto on a fully opaque image emits a rectangle
instead of an image, since the result is identical and much smaller.

Alpha premultiplication is undone before CMYK conversion; skipping that
muddies semi-transparent artwork. There is a test pinning that.

### Verifying PDF output

A hand-rolled writer needs more than unit tests. `TestCrossReferenceTableIsValid`
checks every xref offset lands on the object it claims. Beyond that, render
the file through CoreGraphics — if it produces a thumbnail, the PDF parsed:

```sh
qlmanage -t -s 400 -o render out.pdf   # writes render/out.pdf.png
```

Then look at the PNG: correct orientation, no vertical flip, colours intact.

## Not built yet

- **TUI.** The README wants a TUI over this same core. The CLI must stay
  complete on its own — the README's point is that it be automatable by an
  agent, so no TUI-only capability.
- **`RDG_GLOSS` layer.** A paid add-on. The spot colour constant and its
  alternate CMYK values are defined; nothing generates the layer.
- **`RDG_WHITE_2`** (advanced White → CMYK → White mode). The guide lists it as
  not yet available.
- **Batch mode** for converting all six sides in one invocation.
