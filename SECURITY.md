# Security Policy

## Supported versions

There are no packaged releases yet; the tip of `main` is the supported version.
Rebuild from a fresh clone before reporting.

## What counts

`tayda-uv` runs entirely on your machine. It has no accounts, no network calls
and no telemetry, and it writes nothing but the PDF you asked for. The
interesting surface is:

- **Parsing untrusted files.** `inspect` reads arbitrary PDFs, including ones
  from other tools, and `validate`/`convert` decode PNG, JPEG, GIF and SVG.
  A crafted file that makes the tool do anything beyond report an error is
  in scope. A crash on a damaged file is a bug worth reporting either way;
  the reader is meant to survive corrupt input and move on.
- **Where the PDF is written.** `convert` renders to a temporary file next to
  the destination and renames it into place, resolving symlinks first.
  Anything that lets a destination path write somewhere other than where the
  user pointed it is in scope.
- **Dependencies** pulled in through Go modules and GitHub Actions.

Artwork that validates but prints wrong is not a security problem; it is the
most important kind of bug this project has, and the *Wrong size or rejected
by Tayda* issue form is for it.

## Reporting a vulnerability

Please do not open a public issue for a security problem. Use GitHub's private
reporting instead: **Security → Report a vulnerability** on the repository, or
https://github.com/drlholloway/tayda-uv-artwork-processor/security/advisories/new

Include the Go version, platform, what you observed and how to reproduce it,
with the file if you can share it. You will get an acknowledgement within a
week. Fixes land on `main` with a credit in the commit message unless you
prefer to stay anonymous.

## Dependencies

Dependabot watches the Go module and GitHub Actions dependencies weekly. The
printing path (`internal/enclosure`, `internal/artwork`, `internal/pdfgen`)
uses only the Go standard library, so a third-party advisory can affect the
interactive interface or SVG rendering but not the bytes written to the PDF.
