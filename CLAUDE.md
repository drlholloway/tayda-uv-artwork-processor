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
correct without it. `tayda-uv inspect` checks the file's structure locally and
is **not** a substitute — it says the file is shaped right, not that Tayda will
print it.

### Why the Analyzer is not automated

This was investigated and deliberately not built. <https://pdf.tayda.com> has a
usable REST API — `POST /api/objects/upload` returns a presigned URL, the PDF
is `PUT` there, then `POST /api/analyze` with `{objectPath, fileName, fileSize}`
returns the verdict as JSON. But both endpoints return
`401 {"error":"Not authenticated"}`, and the login they sit behind is wrapped in
reCAPTCHA v3 on `tayda.com` hosts. Driving it from a script means defeating bot
detection Tayda chose to deploy, so the tool does not do it, and a server-side
score threshold could start failing silently at any time even if it did.

`inspect` exists to cover what can be checked honestly and offline. If
programmatic access is wanted, ask Tayda for it rather than working around the
gate.

## Architecture

```
cmd/tayda-uv/        CLI (stdlib flag, subcommands)
  help.go            command descriptions and all help rendering
internal/enclosure/  the artboard dimension table — ground truth
internal/artwork/    decode + validate a source image against a side
internal/pdfgen/     hand-rolled PDF emitter targeting the Tayda spec
internal/pdfinspect/ partial PDF reader, for checking finished files
internal/tui/        Bubble Tea interactive front end
```

Data flows one way: `enclosure` supplies a `Size` in millimetres → `artwork`
checks a decoded image against it → `pdfgen` renders the image onto an
artboard of exactly that size. `enclosure` depends on nothing.

`pdfinspect` sits outside that flow, reading finished PDFs rather than making
them. It imports nothing from this repo on purpose — see below.

**Dependencies are limited to the TUI.** Bubble Tea, Bubbles and Lipgloss are
used by `internal/tui` and nothing else; `enclosure`, `artwork` and `pdfgen`
are stdlib-only and must stay that way. The README's motivation is that the
existing web tool may vanish, so the parts that know how to produce a
printable file should depend on nothing but the Go toolchain. Adding a module
under those three packages needs a reason that outweighs this.

### `cmd/tayda-uv`

Each subcommand is described once, in the `commands` table in `help.go`:
synopsis, summary, notes, examples, and a builder for its flag set. Both the
help output and the running command use that same flag builder, so the two
cannot disagree about what flags exist. **When adding a flag or a subcommand,
add it there** — help is generated, not hand-maintained. The enclosure and
side lists in help come from the catalogue for the same reason.

Conventions worth keeping:

- **Help asked for goes to stdout and exits 0**; help shown because of a
  mistake goes to stderr. `tayda-uv --help | less` should work.
- **Exit 2 means the command was typed wrong, exit 1 means the work failed.**
  A script can tell "no such enclosure" from "this artwork is too low-res".
  Usage mistakes are returned as `*usageError`, which also prints a pointer to
  the relevant `tayda-uv help <command>`.

### `internal/tui`

Bubble Tea. Four screens: choose an enclosure → a table of its six sides →
a file picker → results. `model.go` holds state and transitions, `view.go`
holds rendering; nothing about printing lives here, it only drives the other
packages.

Rules that matter:

- **The TUI must never be able to do something the CLI cannot.** Automating
  this tool is why it exists, per the README. A feature added here needs a
  flag too.
- **Changing enclosure discards attached artwork**, because every side is a
  different size on a different enclosure. Re-picking the same one keeps it.
- **A gloss mask is per-side** (`m` on a row); the global gloss setting cycles
  none → full → artwork and never lands on mask.
- **There is no `-force` equivalent.** The TUI refuses to convert a side whose
  artwork has problems; overriding stays a deliberate command-line act.

Testing: drive `Update` with synthetic `tea.KeyMsg` values and assert on the
returned model — see `model_test.go`. `view_test.go` renders each screen and
`t.Log`s it, so `go test -v ./internal/tui` shows the actual layout. Do look
at it; a screen can pass every assertion and still be laid out wrong.

Running the TUI headlessly under `script`/a bare pty does not work: Bubble Tea
queries the terminal for its background colour and nothing answers, so it
hangs. Verify interactive behaviour by running it in a real terminal.

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
- White is painted **before** CMYK and gloss **after**, matching the default
  White → CMYK → Gloss print order. The guide's checklist calls this order
  "VERY IMPORTANT"; `TestPrintOrderIsWhiteThenCMYKThenGloss` decompresses the
  finished content stream and reads the order back out rather than trusting
  the builder.
- Nothing is drawn outside the page box.

White and gloss are the same shape of thing — an ink layer painted through a
`Separation` colour space — so they share one `coating` type and one code
path. A coating is either a flood-filled rectangle (uniform full coverage) or
a coverage image carrying one 8-bit tint per pixel.

`WhiteMode`: `none` (white enclosures), `full`, `auto` (follow the artwork's
alpha, so transparent areas get no undercoat). `GlossMode`: `none` (default —
it is a paid add-on), `full`, `artwork`, `mask`. Auto/artwork on a fully
opaque image emits a rectangle rather than an image, since the result is
identical and much smaller.

Two encoding traps, each with a test pinning it:

- **Alpha premultiplication is undone before CMYK conversion.** Skipping it
  muddies semi-transparent artwork.
- **Coverage images carry an `/SMask` equal to their coverage.** A `Separation`
  image paints its *alternate* space, and zero tint there is white, not
  nothing — so without the mask, the uncoated part of a layer covers
  everything beneath it in opaque white. This is invisible for a white
  undercoat (white on white) and obvious the moment gloss sits on top, which
  is how it was found. Alpha must track tint.

Gloss mask coverage is read from alpha when the mask has transparency, and
from Rec. 601 luma otherwise (white coats, black leaves bare). The mask is
scaled to the artboard independently of the artwork, so the two need not share
pixel dimensions.

### `internal/pdfinspect`

A partial PDF reader behind `tayda-uv inspect`. It reports the artboard in mm,
the colour spaces present, the `Separation` ink names, the optional content
group names, and the order the content stream actually paints the layers in.

**It is written from the PDF spec, not from `pdfgen`, and imports nothing from
this repo.** That independence is the whole point: `pdfgen`'s own tests can
only catch mistakes it is inconsistent about, never one it makes the same way
every time. A reader that shared the writer's assumptions would reproduce them
instead of catching them. The round-trip tests in `roundtrip_test.go` are what
make the pair worth having — mutate the paint order in `pdfgen` and they fail.

Design rules:

- **Never report "clean" about a file it failed to parse.** Encryption, an
  unsupported filter, a missing page tree and an unknown predictor are all
  errors or `Notes`, never silence. A checker that passes files it did not
  understand is worse than no checker.
- **Do not follow the cross-reference table.** It scans for `N G obj` headers
  instead, so a damaged xref is still inspectable — and that is exactly the
  file worth being able to look at. Stream payloads are skipped over so image
  bytes that happen to spell an object header cannot shadow a real object;
  `TestStreamBytesCannotInventAnObject` pins this.
- **Object streams are unpacked** (`/Type/ObjStm`). Illustrator and Inkscape
  pack most objects that way; without it their files look nearly empty.
- **Names and strings are decoded properly** — `#5F` escapes and octal string
  escapes both. `RDG#5FWHITE` is `RDG_WHITE`, and a reader that missed that
  would report a spot colour that does not exist.

It knows nothing about enclosures. Matching an artboard to a side is
`enclosure.MatchSize`, which returns every side of that size — normally more
than one, since A equals the Lid, B equals D and C equals E.

### Verifying PDF output

A hand-rolled writer needs more than unit tests. `TestCrossReferenceTableIsValid`
checks every xref offset lands on the object it claims. Beyond that, render
the file through CoreGraphics — if it produces a thumbnail, the PDF parsed:

```sh
qlmanage -t -s 400 -o render out.pdf   # writes render/out.pdf.png
```

Then look at the PNG: correct orientation, no vertical flip, colours intact.

## Not built yet

- **`RDG_WHITE_2`** (advanced White → CMYK → White mode). The guide lists it as
  not yet available.
- **"Print white layer twice"** add-on. Not represented in the file.
- **Batch mode** for converting all six sides in one invocation.
