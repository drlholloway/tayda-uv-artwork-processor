package pdfgen

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// WriteFile renders a job beside its destination and moves it into place only
// once the whole file is there.
//
// Build takes an io.Writer and streams nothing until the end, so opening the
// destination directly would create and truncate it before a byte was
// emitted: any failure inside Build leaves a zero-length PDF behind, and a
// failure while writing leaves a partial one. Either is a file named like a
// finished artboard that is not one.
//
// This tool exists because nothing downstream checks the file — Tayda prints
// what they are sent — so a broken PDF sitting next to good ones under a
// plausible name is exactly the one that gets uploaded by mistake. It matters
// most when several sides are converted in a batch, where one failure would
// otherwise leave an empty PDF among five real ones. Either the file appears
// complete or it does not appear.
//
// Callers that want the bytes somewhere other than a file still use Build.
func WriteFile(j Job, path string) (err error) {
	// In the destination's own directory, so the rename is a rename and not a
	// copy across filesystems. The leading dot keeps a leftover out of the way
	// if the process is killed between the two.
	f, err := os.CreateTemp(filepath.Dir(path), ".tayda-uv-*.pdf")
	if err != nil {
		// Reported against the destination. The temporary name is an
		// implementation detail, and naming a file the user never asked for
		// sends them looking in the wrong place.
		var pe *os.PathError
		if errors.As(err, &pe) {
			return fmt.Errorf("write %s: %w", path, pe.Err)
		}
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmp)
		}
	}()

	if err = Build(j, f); err != nil {
		return err
	}
	// Closed explicitly so a write error on flush is reported rather than
	// swallowed by a deferred close.
	if err = f.Close(); err != nil {
		return err
	}
	// CreateTemp opens at 0600, where os.Create would have given the umask its
	// say. Match what these files used to end up as rather than quietly
	// tightening them on a shared output directory.
	if err = os.Chmod(tmp, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
