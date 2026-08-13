---
name: tayda-uv
description: Prepare pedal-enclosure artwork for the Tayda UV printing service — validate an image against an enclosure side, emit the print-ready PDF (CMYK, RDG_WHITE undercoat, optional RDG_GLOSS varnish), and inspect a finished PDF to confirm its artboard, spot colours and print order. Use whenever the user mentions Tayda UV printing, printing on a 1590B/125B/1590XX-style enclosure, guitar-pedal enclosure artwork, artboard sizes for an enclosure side, or asks whether an image is print-ready for a pedal face, whether a PDF is correct for Tayda, or wants a PDF for the Tayda Box Tool.
---

# Tayda UV artwork

`tayda-uv` turns an image into the PDF Tayda's UV printer expects: an artboard
sized exactly to one enclosure side, artwork in DeviceCMYK, and the spot-colour
ink layers painted in the required White → CMYK → Gloss order.

**Tayda prints the file exactly as submitted and does not correct it.** A wrong
artboard is a ruined enclosure the user pays for. So: validate before writing,
never pass `-force` on your own initiative, and tell the user to run the output
through Tayda's PDF Analyzer before ordering.

## Getting the binary

`tayda-uv` is installed to `~/go/bin`, which is on PATH. Confirm with:

```sh
tayda-uv enclosures
```

If that fails, reinstall from the source repo (on this machine,
`~/Code/tayda-uv-artwork-processor`). Go lives at `/opt/homebrew/bin/go` and is
often missing from a non-login shell's PATH, so set it explicitly:

```sh
PATH=/opt/homebrew/bin:$PATH go install ./cmd/tayda-uv
```

## Workflow

Work one side at a time; each PDF holds a single artboard, which is what the
Tayda Box Tool expects.

**1. Get the target size.** The user's image has to already be the right shape;
the tool scales to fill and will not crop for them.

```sh
tayda-uv sides 1590B
```

```
SIDE  ARTBOARD       MIN PIXELS @300DPI  TOLERANCE
A     56 × 108.5 mm  662 × 1282          ±0.50 mm
B     52 × 24 mm     615 × 284           ±1.00 mm
...
```

Side A is the face, Lid is the back plate. A = Lid, B = D, C = E on every
enclosure. Enclosures: `125B 1590A 1590B 1590BB 1590BB2 1590D 1590DD 1590XX`.
Tolerance is Tayda's printer registration accuracy, not slack in the artboard.

**2. Validate.** Exit 0 means ready, exit 1 means do not print it.

```sh
tayda-uv validate -e 1590B -s A face.png
```

```
artwork face.png: PNG, 700 × 1356 px
  side A: 56 × 108.5 mm, artwork lands at 318 × 317 DPI
  warning: aspect ratio is off by 0.02%, within tolerance; artwork will be scaled to fill the artboard
```

`problem:` lines block printing: under 300 DPI at final size, or aspect drift
over 0.5% (the artwork would be visibly stretched). `warning:` lines do not
block but are worth repeating — small aspect drift, and a portrait/landscape
mismatch that usually means the artwork is rotated 90°. Report problems and
stop; fixing them means re-exporting at a higher resolution or the right aspect,
which is the user's call.

A gloss mask is held to a looser standard than artwork — low resolution on a
mask is only a warning, since it is coverage rather than image detail.

**3. Convert.**

```sh
tayda-uv convert -e 1590B -s A -white full -o face.pdf face.png
```

Output defaults to a lowercased `<image>-<enclosure>-<side>.pdf`
(`face-1590b-a.pdf`) if `-o` is omitted. The success line names the layers
written, e.g. `layers: RDG_WHITE → CMYK`.

`convert` re-runs validation itself and refuses to write when there are
problems, so a separate `validate` step is only needed when you want to check
without producing a file.

**4. Inspect the result.** This is your programmatic gate — run it after every
convert. It re-reads the finished PDF and checks it independently of the code
that wrote it. Exit 0 clean, 1 if anything is wrong.

```sh
tayda-uv inspect -e 1590B -s A face.pdf
```

```
face.pdf: 1 page, 158.74 × 307.56 pt
  artboard: 56 × 108.5 mm            1590B side A OK
  colour:   DeviceCMYK               no RGB OK
  spots:    RDG_WHITE                OK
  order:    RDG_WHITE → CMYK         OK
```

Always pass `-e`/`-s` when you know them — without them it only checks the
artboard is *some* valid side, which will happily pass a file built for the
wrong one. It accepts several PDFs at once and reports on each.

**5. Look at it, and tell the user to verify.** `inspect` proves the file's
structure, not that the artwork is the right way up or the colours are what
anyone pictured. Render it and actually read the image:

```sh
qlmanage -t -s 400 -o render face.pdf   # writes render/face.pdf.png
```

Check orientation, no vertical flip, colours intact. Then point the user at
Tayda's PDF Analyzer (<https://pdf.tayda.com>, user `pdfman`, pass `pdfman`),
which is the final authority, and remind them to order one enclosure first.

## Choosing `-white`

CMYK has no white ink, so on a coloured enclosure the artwork sinks into the
powder coat without an undercoat under it. This depends on the physical
enclosure the user bought — **ask if you don't know its colour.**

| Enclosure | `-white` |
|---|---|
| Dark or coloured powder coat | `full` (flood) or `auto` |
| Raw / unfinished aluminium | `full` or `auto` |
| White powder coat | `none` |

`auto` (the default) follows the artwork's alpha, so transparent regions get no
undercoat and the enclosure colour shows through — that is what you want for a
logo on bare metal. `full` floods the whole side and is what you want when the
artwork should sit on a solid white ground.

## Choosing `-gloss`

Varnish is a **paid Tayda add-on**, so it is `none` unless the user asks for it.
Do not turn it on to be helpful.

- `full` — varnish the whole side
- `artwork` — varnish wherever the artwork is opaque
- `mask` — varnish where a separate image says; `-gloss-mask logo.png` implies this

A mask is read as coverage: alpha if it has transparency, otherwise brightness
(white coats, black leaves bare). It is scaled to the artboard on its own, so it
need not match the artwork's pixel dimensions — but it **must still match the
artboard's aspect ratio**, or it is stretched and the conversion is refused. Low
resolution on a mask is only a warning; wrong shape is a problem. When making a
mask for the user, size it to the artwork's own dimensions and it will be right.

Whether that varnish comes out gloss or matte is chosen in the Tayda Box Tool at
upload time; it is not recorded in the PDF. Say so rather than implying the file
decides.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | worked |
| 1 | the work failed — artwork not printable, unreadable image, write error |
| 2 | the command was typed wrong — unknown enclosure or side, missing flag |

There is no batch mode, so doing every side is a loop. `convert` validates on
its own and skips what it cannot print correctly; `inspect` confirms what it
wrote:

```sh
for s in A B C D E Lid; do
  tayda-uv convert -e 1590B -s "$s" -white full -o "out-$s.pdf" "art-$s.png" &&
  tayda-uv inspect -e 1590B -s "$s" "out-$s.pdf"
done
```

Collect the sides that exited non-zero and report them together rather than
stopping at the first one.

## Rules

- **Never pass `-force` unless the user explicitly asks after seeing the
  problems.** It writes a PDF that failed validation, and Tayda will print it.
- **One command per side.** Don't try to combine sides into one PDF.
- **Don't invent artboard sizes.** They come from `tayda-uv sides`, which is
  transcribed from the guide. If a size looks wrong, re-read the live guide
  (<https://www.taydaelectronics.com/uv-printing-service-guide-v1>) in a browser
  — it 403s plain HTTP fetches — rather than adjusting numbers.
- **Recommend ordering a single enclosure before a batch.**
- **Don't try to script Tayda's PDF Analyzer.** It has a REST API, but the
  endpoints are authenticated and the login is behind reCAPTCHA. Uploading the
  file is the user's step, in their browser. `inspect` is the automatable check;
  it is not the same thing and must not be described as Tayda's approval.

## Interactive alternative

`tayda-uv` with no arguments opens a TUI (enclosure → sides table → file picker
→ results). Suggest it when the user is doing all six sides by hand. Don't try
to drive it yourself — it needs a real terminal, and everything it does is
available as a flag.
