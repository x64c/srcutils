package main

import (
	"fmt"
	"os"
)

func stderr(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format, args...)
}

// die prints a message to stderr and exits with the given code.
func die(code int, format string, args ...any) {
	stderr(format, args...)
	os.Exit(code)
}
