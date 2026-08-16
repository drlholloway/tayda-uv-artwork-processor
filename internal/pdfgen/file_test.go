package pdfgen

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFileProducesAWholeFile(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "face.pdf")

	err := WriteFile(Job{
		Image: opaqueImage(16, 31), WidthMM: 56, HeightMM: 108.5,
		White: WhiteAuto, Gloss: GlossNone,
	}, out)
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "%PDF") || !strings.HasSuffix(string(b), "%%EOF\n") {
		t.Error("the file that landed is not a complete PDF")
	}
	assertNoLeftovers(t, dir)
}

// The reason for the temp-and-rename: a failed convert must not leave a
// zero-length PDF sitting under a plausible name, because nothing downstream
// checks the file and that is the one that gets uploaded by mistake.
func TestWriteFileLeavesNothingBehindWhenItFails(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "face.pdf")

	// A job with no image fails inside Build, after the destination would
	// have been created and truncated by the old code.
	if err := WriteFile(Job{WidthMM: 56, HeightMM: 108.5}, out); err == nil {
		t.Fatal("a job with no image was written anyway")
	}

	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("%s exists after a failed write; it should never have appeared", out)
	}
	assertNoLeftovers(t, dir)
}

// An existing file is only replaced by a complete one.
func TestWriteFileDoesNotDestroyAGoodFileOnFailure(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "face.pdf")

	const previous = "%PDF-1.7 an earlier, working export\n%%EOF\n"
	if err := os.WriteFile(out, []byte(previous), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := WriteFile(Job{WidthMM: 56, HeightMM: 108.5}, out); err == nil {
		t.Fatal("a job with no image was written anyway")
	}

	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != previous {
		t.Error("a failed convert damaged the file that was already there")
	}
	assertNoLeftovers(t, dir)
}

// Failures are reported against the destination, never the temporary file —
// which the cleanup has already deleted, so naming it sends the user looking
// for something that exists nowhere. This has to hold on every path out, not
// just the one where the temporary file could not be created: a disk filling
// up mid-write surfaces from deep inside Build carrying the temporary path.
func TestWriteFileNeverNamesTheTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	good := Job{Image: opaqueImage(16, 31), WidthMM: 56, HeightMM: 108.5}

	for _, c := range []struct {
		name string
		job  Job
		out  string
	}{
		{"the temporary file cannot be created", good, filepath.Join(dir, "no-such-dir", "face.pdf")},
		{"rendering fails", Job{WidthMM: 56, HeightMM: 108.5}, filepath.Join(dir, "face.pdf")},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := WriteFile(c.job, c.out)
			if err == nil {
				t.Fatal("the write succeeded")
			}
			if !strings.Contains(err.Error(), c.out) {
				t.Errorf("error does not name the destination: %v", err)
			}
			if strings.Contains(err.Error(), ".tayda-uv-") {
				t.Errorf("error leaks the temporary file name: %v", err)
			}
		})
	}
}

// The two error shapes the os package actually returns for the failures that
// matter here, neither of which a test can provoke: the disk filling up
// mid-write, and a rename that cannot complete. Both arrive carrying the
// temporary path, which is the whole reason destError exists.
func TestDestErrorReplacesTheTemporaryPath(t *testing.T) {
	const (
		dest = "/out/face.pdf"
		tmp  = "/out/.tayda-uv-2843921.pdf"
	)
	for _, in := range []error{
		&fs.PathError{Op: "write", Path: tmp, Err: errors.New("no space left on device")},
		&os.LinkError{Op: "rename", Old: tmp, New: dest, Err: errors.New("file exists")},
	} {
		got := destError(dest, in).Error()
		if strings.Contains(got, tmp) {
			t.Errorf("%T still names the temporary file: %s", in, got)
		}
		if !strings.Contains(got, dest) {
			t.Errorf("%T does not name the destination: %s", in, got)
		}
	}
}

// os.Create left an existing file's permissions alone. Renaming over one must
// not widen them, or re-converting would publish a PDF someone had made
// private.
func TestWriteFileKeepsAnExistingFilesPermissions(t *testing.T) {
	out := filepath.Join(t.TempDir(), "face.pdf")
	if err := os.WriteFile(out, []byte("%PDF old\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	job := Job{Image: opaqueImage(16, 31), WidthMM: 56, HeightMM: 108.5}
	if err := WriteFile(job, out); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("permissions became %v, want the 0600 the file already had", got)
	}
}

// A symlink destination is written through, not replaced: someone who points
// the output at a link into a synced folder means the PDF to land there.
func TestWriteFileWritesThroughASymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.pdf")
	link := filepath.Join(dir, "face.pdf")
	if err := os.WriteFile(target, []byte("%PDF old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	job := Job{Image: opaqueImage(16, 31), WidthMM: 56, HeightMM: 108.5}
	if err := WriteFile(job, link); err != nil {
		t.Fatal(err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a regular file")
	}
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "%PDF") || !strings.HasSuffix(string(b), "%%EOF\n") {
		t.Error("the PDF did not land on the far side of the link")
	}
	assertNoLeftovers(t, dir)
}

// The temporary file is an implementation detail and must not outlive the
// call, whether it succeeded or not.
func assertNoLeftovers(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tayda-uv-") {
			t.Errorf("temporary file %s was left behind", e.Name())
		}
	}
}
