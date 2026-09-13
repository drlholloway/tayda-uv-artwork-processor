package main

import "testing"

// The release workflow injects the version with -ldflags; that has to win
// over whatever the toolchain recorded, or every release would call itself
// by its commit.
func TestVersionFlagOverridesBuildInfo(t *testing.T) {
	defer func(v string) { version = v }(version)
	version = "1.2.3"
	if got := versionString(); got != "1.2.3" {
		t.Fatalf("versionString() = %q, want 1.2.3", got)
	}
}

// Without an injected version the tool must still say something a bug
// report can use, never an empty string.
func TestVersionStringIsNeverEmpty(t *testing.T) {
	defer func(v string) { version = v }(version)
	version = ""
	if got := versionString(); got == "" {
		t.Fatal("versionString() is empty")
	}
}
