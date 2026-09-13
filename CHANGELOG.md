# Changelog

All notable changes to `tayda-uv`. The section for a tagged version becomes
the GitHub Release notes.

## Unreleased

## 1.0.0 — 2026-09-13

First release. A test enclosure has been printed from its output and came
back as drawn.

### Added
- **Enclosures**: 125B, 1590A, 1590B, 1590BB, 1590BB2, 1590D, 1590DD and
  1590XX, all six sides each, transcribed from V2 (22 April 2026) of Tayda's
  file preparation guide. `tayda-uv sides` prints each side's artboard, the
  pixel minimum for 300 DPI and Tayda's printing tolerance.
- **`validate`** checks an image against a side: 300 DPI at physical size,
  aspect ratio within 0.5%, and a warning when the artwork looks rotated 90°.
  Exit codes tell a script "not printable" (1) from "typed wrong" (2).
- **`convert`** writes the print-ready PDF: one artboard at the exact size,
  artwork in DeviceCMYK with no RGB anywhere, `RDG_WHITE` undercoat and
  optional `RDG_GLOSS` varnish as spot colours, painted White → CMYK → Gloss.
  `-white none|auto|full`, `-gloss none|full|artwork|mask`, `-gloss-mask`
  for coating one element, and `-force` for printing anyway after reading
  the problems. Gloss coverage is reported with the guide's fingerprint
  warning above 50%. The PDF is written atomically, so a failed conversion
  leaves nothing behind.
- **`inspect`** reads a finished PDF back, independently of the code that
  wrote it, and reports the artboard and which sides it matches, the colour
  spaces, the spot-colour names and the actual paint order. Works on PDFs
  from other tools too, and refuses to call a file clean that it could not
  fully read.
- **SVG artwork**, rasterised for the target side at 600 DPI (`-dpi`). Text,
  embedded images, clips, masks, filters and patterns are refused with a
  message saying what to do, rather than silently dropped.
- **Interactive interface**: run with no arguments, choose an enclosure,
  attach artwork to each side with a file picker, set white and gloss, and
  convert every side in one pass. Everything it does is also a flag.
- **`version`** command, for bug reports.
- A [Claude Code skill](.claude/skills/tayda-uv/) that teaches an agent the
  validate → convert → inspect workflow and the rules around `-force`,
  gloss and Tayda's Analyzer.
- A [wiki](https://github.com/drlholloway/tayda-uv-artwork-processor/wiki)
  with installation, first steps, preparing artwork, every enclosure's
  sizes, white and gloss, the command line, checking the output and
  troubleshooting.
