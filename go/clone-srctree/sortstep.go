package main

import "github.com/x64c/srcutils/go/sort-imports/sortimports"

// initSortImports loads the stdlib list once at startup (runs `go list std`,
// inheriting the process env so GOEXPERIMENT is respected). Fails fast.
func initSortImports() {
	if err := sortimports.LoadStdlib(); err != nil {
		die(4, "sort-imports: load stdlib: %v\n", err)
	}
}

// sortStep fixes import order across the target (real runs only) and returns a
// one-line status for the summary.
func sortStep(to string, dry bool) string {
	if dry {
		return "would run"
	}
	if err := sortimports.ProcSrcTree(to); err != nil {
		die(4, "sort-imports: %v\n", err)
	}
	return "ok"
}
