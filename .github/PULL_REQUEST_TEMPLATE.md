## What this changes

<!-- One or two sentences. Link the issue it addresses, if any: "Fixes #12". -->

## How it was tested

- [ ] `go test ./...` passes locally
- [ ] `go vet ./...` is clean and `gofmt -l .` prints nothing
- [ ] Tried on: <!-- macOS / Linux / Windows, or "unit tests only" -->

## Checklist

- [ ] Changes to what is written into the PDF come with a test in `internal/pdfgen`, and a round-trip test in `internal/pdfinspect` where the reader can see the change
- [ ] `internal/enclosure`, `internal/artwork` and `internal/pdfgen` still import only the standard library, and `internal/pdfinspect` imports nothing from this repo
- [ ] A new flag or subcommand is in the `commands` table in `cmd/tayda-uv/help.go`; a new TUI feature has a matching flag
- [ ] Dimension changes cite the Tayda guide's version and date
- [ ] The wiki page showing any changed flag, key, size or message is updated, or this PR says it needs to be
