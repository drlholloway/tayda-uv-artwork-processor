package pdfgen

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteFile renders a job to a file, and only lets a complete one appear
// under the name that was asked for.
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
func WriteFile(j Job, path string) error {
	// A symlink is written through rather than replaced. os.Create followed
	// it, and someone who pointed the output at a link into a synced folder
	// meant the PDF to land on the far side of it, not to lose the link.
	// EvalSymlinks fails when nothing is there yet, which is the ordinary
	// case and needs no resolving.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}

	// A destination that is not a regular file — a device, a fifo — cannot be
	// renamed over, and was never at risk of being left half-written under a
	// plausible name either. Write straight through, as os.Create did.
	if info, err := os.Stat(path); err == nil && !info.Mode().IsRegular() {
		return writeThrough(j, path)
	}
	return writeAtomic(j, path)
}

// writeAtomic renders beside the destination and renames into place.
func writeAtomic(j Job, path string) (err error) {
	// Every failure below is reported against the destination. The temporary
	// name is an implementation detail, and one the cleanup has already
	// removed, so naming it sends the user looking for a file that exists
	// nowhere — the disk-full case being the one that matters, since that
	// error arrives from deep inside the write carrying the temporary path.
	defer func() {
		if err != nil {
			err = destError(path, err)
		}
	}()

	// In the destination's own directory, so the rename is a rename and not a
	// copy across filesystems. The leading dot keeps a leftover out of the way
	// if the process is killed between the two.
	f, err := os.CreateTemp(filepath.Dir(path), ".tayda-uv-*.pdf")
	if err != nil {
		return err
	}
	tmp := f.Name()
	// Runs before the wrapping defer above, so the temporary file is gone by
	// the time the error naming it is rewritten.
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
	if err = os.Chmod(tmp, destMode(path)); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// writeThrough renders into an already-open-able destination, for the
// non-regular files a rename cannot replace.
func writeThrough(j Job, path string) (err error) {
	defer func() {
		if err != nil {
			err = destError(path, err)
		}
	}()

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	return Build(j, f)
}

// destMode is the permission the finished file should carry.
//
// os.Create left an existing file's permissions alone, so replacing one must
// not widen them — re-converting over a PDF someone had made private would
// otherwise publish it. Only when there is nothing there does this choose,
// and 0644 is that choice: os.CreateTemp opens at 0600, which is a surprise
// for output meant to be handed to a printer. That is a decision rather than
// a reproduction of os.Create, whose result depended on the umask.
func destMode(path string) fs.FileMode {
	if info, err := os.Stat(path); err == nil {
		return info.Mode().Perm()
	}
	return 0o644
}

// destError reports a failure against the file the user asked for, dropping
// the temporary path the underlying os error carries.
func destError(path string, err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return fmt.Errorf("write %s: %w", path, pe.Err)
	}
	// os.Rename reports a LinkError, which names both paths.
	var le *os.LinkError
	if errors.As(err, &le) {
		return fmt.Errorf("write %s: %w", path, le.Err)
	}
	return fmt.Errorf("write %s: %w", path, err)
}
