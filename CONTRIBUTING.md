# Contributing to tayda-uv

Thanks for your interest. `tayda-uv` is a small project maintained by one
person, so the most useful contributions are focused ones: a clear bug report,
a file Tayda's Analyzer rejected that `inspect` passed, a change in the Tayda
guide with a link to the live page, or a fix with a test.

By participating you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).

## Ways to help

- **Report a bug.** Use the *Bug report* issue form. Say which enclosure and
  side, and what the tool printed; most behaviour depends on them.
- **Report a wrong artboard or a rejected file.** Use the *Wrong size or
  rejected by Tayda* form. The enclosure, the side, what the tool wrote and
  what Tayda's Analyzer said are enough to turn it into a test.
- **Suggest a feature.** Use the *Feature request* form. Check the "Not built
  yet" section of `CLAUDE.md` first; it lists what is known to be missing and
  why some of it is missing on purpose.
- **Improve the wiki.** It is a normal GitHub wiki; edits are welcome. British
  spelling ("colour"), to match the tool's own output.
- **Keep the dimension table current.** Tayda revises their guide. If the live
  page disagrees with `tayda-uv sides`, open an issue with a link, or send a
  PR against `internal/enclosure` with the guide's version and date in the
  commit message.

## Development setup

Requirements: Go 1.26 or newer. Nothing else; there is no code generation and
no native toolchain.

```sh
git clone https://github.com/drlholloway/tayda-uv-artwork-processor.git
cd tayda-uv-artwork-processor
go build ./...
go test ./...
go vet ./... && gofmt -l .          # gofmt -l must print nothing
go build -o tayda-uv ./cmd/tayda-uv
```

The layout is described in `CLAUDE.md`. In short: `internal/enclosure` is the
dimension table, `internal/artwork` validates an image against a side,
`internal/pdfgen` writes the PDF, `internal/pdfinspect` reads one back, and
`cmd/tayda-uv` and `internal/tui` are the two front ends.

## Pull requests

1. Open an issue first for anything beyond a small fix, so the approach can be
   agreed before you spend time on it.
2. Branch from `main`. Keep a PR to one change.
3. Add or update tests. A change to what gets written into the PDF needs a
   test in `internal/pdfgen`, and usually a round-trip test in
   `internal/pdfinspect` too.
4. Run `gofmt -l .`, `go vet ./...` and `go test ./...`. CI runs the same on
   Linux and macOS.
5. Keep `enclosure`, `artwork` and `pdfgen` stdlib-only. The point of the tool
   is that the part which produces a printable file depends on nothing but the
   Go toolchain. Adding a module under those three packages needs a reason
   that outweighs this, stated in the PR.
6. `pdfinspect` must not import anything from this repository. It is the
   independent check on `pdfgen`, and sharing code would defeat it.
7. Adding a flag or subcommand means adding it to the `commands` table in
   `cmd/tayda-uv/help.go`; help is generated from there. A feature added to
   the TUI needs a flag too, so the CLI can always do what the TUI can.
8. Update the wiki page that shows the changed flag, key, size or message, or
   say in the PR that it needs updating.
9. Add a line under **Unreleased** in `CHANGELOG.md` if users would notice
   the change.
10. Fill in the PR template.

## Releases

Maintainer only. Move the **Unreleased** section of `CHANGELOG.md` under a
new `## X.Y.Z — date` heading, commit, then tag that commit `vX.Y.Z` and
push the tag. The release workflow runs the tests, builds macOS, Linux and
Windows archives with the version baked in, and publishes a GitHub Release
whose notes are that changelog section.

## Style

British spelling in user-facing text and docs ("colour", "rasterise"),
matching the tool's output. `gofmt` decides the rest.

## Licensing of contributions

`tayda-uv` is licensed under the [MIT License](LICENSE). By submitting a
contribution you agree that it is licensed under the same terms. Please do not
submit code copied from projects under incompatible licenses.

## Questions

Open an issue with the *question* label.
