// Package toolver reports a tool's version from the build info embedded by the
// Go toolchain — the same truth `go version -m <binary>` reads. Installed
// binaries report their exact module tag; source builds report (devel).
package toolver

import (
	"fmt"
	"path"
	"runtime"
	"runtime/debug"
)

func String() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown (no build info)"
	}
	return fmt.Sprintf("%s %s (%s, %s)",
		path.Base(bi.Main.Path), bi.Main.Version, bi.Main.Path, runtime.Version())
}

func Print() {
	fmt.Println(String())
}
