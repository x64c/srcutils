package main

import (
	"fmt"
	"os"
)

func stderr(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format, args...)
}
