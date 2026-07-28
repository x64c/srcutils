package sortimports

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ProcSrcFile reads a Go source file, sorts its imports,
// and atomically replaces the file only if imports changed.
// Returns true if the file was modified.
func ProcSrcFile(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	r := parseSrcImports(lines)
	if !r.hasImports() {
		return false, nil
	}

	formatted := r.groups.formatLines()

	// Check if imports are already sorted
	oldImportLines := lines[r.startLine:r.endLine]
	if linesEqual(oldImportLines, formatted) {
		return false, nil
	}

	// Write to temp file in the same directory (for atomic rename)
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".sort-imports-*.go")
	if err != nil {
		return false, fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		// Clean up temp file on failure
		_ = os.Remove(tmpPath)
	}()

	w := bufio.NewWriter(tmp)
	writeLine := func(s string) error {
		if _, err := w.WriteString(s); err != nil {
			return err
		}
		return w.WriteByte('\n')
	}
	writeLineNoNewline := func(s string) error {
		_, err := w.WriteString(s)
		return err
	}

	// Write lines before imports
	for i := 0; i < r.startLine; i++ {
		if err := writeLine(lines[i]); err != nil {
			_ = tmp.Close()
			return false, fmt.Errorf("write: %w", err)
		}
	}

	// Write sorted imports
	for _, line := range formatted {
		if err := writeLine(line); err != nil {
			_ = tmp.Close()
			return false, fmt.Errorf("write: %w", err)
		}
	}

	// Write lines after imports
	for i := r.endLine; i < len(lines); i++ {
		if i < len(lines)-1 {
			err = writeLine(lines[i])
		} else {
			err = writeLineNoNewline(lines[i])
		}
		if err != nil {
			_ = tmp.Close()
			return false, fmt.Errorf("write: %w", err)
		}
	}

	if err := w.Flush(); err != nil {
		_ = tmp.Close()
		return false, fmt.Errorf("flush: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return false, fmt.Errorf("close temp: %w", err)
	}

	// Preserve original file permissions
	info, err := os.Stat(path)
	if err != nil {
		return false, fmt.Errorf("stat: %w", err)
	}
	if err := os.Chmod(tmpPath, info.Mode()); err != nil {
		return false, fmt.Errorf("chmod: %w", err)
	}

	// Atomic replace
	if err := os.Rename(tmpPath, path); err != nil {
		return false, fmt.Errorf("rename: %w", err)
	}

	return true, nil
}

// linesEqual compares two string slices.
func linesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
