package pdfgen

import (
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

// An unwritable destination is reported as the destination, not as the
// temporary file the user never asked for and cannot go looking for.
func TestWriteFileNamesTheDestinationWhenItCannotBeCreated(t *testing.T) {
	out := filepath.Join(t.TempDir(), "no-such-dir", "face.pdf")

	err := WriteFile(Job{
		Image: opaqueImage(16, 31), WidthMM: 56, HeightMM: 108.5,
	}, out)
	if err == nil {
		t.Fatal("writing into a missing directory succeeded")
	}
	if !strings.Contains(err.Error(), out) {
		t.Errorf("error does not name the destination: %v", err)
	}
	if strings.Contains(err.Error(), ".tayda-uv-") {
		t.Errorf("error leaks the temporary file name: %v", err)
	}
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
