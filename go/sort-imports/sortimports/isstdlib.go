package sortimports

import (
	"os/exec"
	"strings"
)

var stdlibSet map[string]struct{}

// LoadStdlib runs `go list std` and builds a set of stdlib package paths.
// Inherits the caller's environment (e.g. GOEXPERIMENT=jsonv2).
// Filters out vendor/ entries which are internal to the stdlib.
func LoadStdlib() error {
	out, err := exec.Command("go", "list", "std").Output()
	if err != nil {
		return err
	}
	stdlibSet = make(map[string]struct{})
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(line, "vendor/") {
			continue
		}
		stdlibSet[line] = struct{}{}
	}
	return nil
}

// isStdlib returns true if the import path is a Go standard library package.
func isStdlib(importPath string) bool {
	_, ok := stdlibSet[importPath]
	return ok
}
