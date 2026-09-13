package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
)

// version is set by the release build with
// -ldflags "-X main.version=1.0.0". Left empty, versionString falls back to
// what the Go toolchain recorded, so a `go install` from a clone still
// identifies itself well enough for a bug report.
var version string

func versionString() string {
	if version != "" {
		return version
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "devel"
	}
	// `go install module@vX.Y.Z` records the module version.
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	// A build from a clone records the commit it was made from.
	var rev, dirty string
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	if rev == "" {
		return "devel"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	return "devel-" + rev + dirty
}

func cmdVersion(args []string) error {
	if wantsHelp(args) {
		c, _ := lookupCommand("version")
		c.printUsage(os.Stdout)
		return nil
	}
	if len(args) != 0 {
		return &usageError{cmd: "version", msg: "version takes no arguments"}
	}
	fmt.Printf("tayda-uv %s %s/%s\n", versionString(), runtime.GOOS, runtime.GOARCH)
	return nil
}
