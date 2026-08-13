# Tayda UV Artwork Processor
### From Image File to Prepared PDF for Tayda UV Printing Service

## Introduction

The Tayda UV Printing process is a God-send for small pedal builders wanting to create professional looking pedals on a budget. They can go from nothing to a fully professionally finished enclosure in short time and with little money invested. One of the most difficult parts of the process however is to prepare the artwork for printing.

There exists a tool already that handles the front face creation of the needed pdf here: https://forge.optictonelab.com/. However, there is always a chance that it can disappear or not be updated. This is a TUI program that you select the enclosure type, the images for each side and then run the conversion for each side. It can also be run fully from the command line so it can be automated via an agent skill.

The tool verifies the sizes of the image is correct for the enclosure and ensures that the output is correct for tayda.

## Status

The command line side works: enclosure/side lookup, artwork validation, and
PDF generation. The TUI is not built yet.

## Install

```sh
go build -o tayda-uv ./cmd/tayda-uv
```

No third-party dependencies — the Go toolchain is all you need.

## Usage

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
  coverage percentage and warns above 50%.
- Whether the finish is gloss varnish or gloss matte is **not** in the PDF —
  you choose it in the Tayda Box Tool when saving the template.

### Validation

`convert` validates before writing and refuses to produce a file that will not
print correctly. It checks that the artwork resolves to at least 300 DPI at
physical size, that its aspect ratio matches the artboard, and warns when an
image looks rotated 90°. Use `-force` to write anyway.

`validate` exits 0 when the artwork is ready and 1 when it is not, so it works
in a script. Exit 2 always means the command itself was typed wrong.

Validation is not a substitute for Tayda's own
[PDF Analyzer](https://www.taydaelectronics.com/uv-printing-service-guide-v1)
(user `pdfman` / pass `pdfman`). Always order a small quantity first.

## References
[Tayda UV Printing Service Guide](https://www.taydaelectronics.com/uv-printing-service-guide-v1)
— currently serving V2 (April 22 2026) despite the `v1` URL. The artboard
dimensions in `internal/enclosure` are transcribed from its table.

