package sortimports

import "strings"

// extractImportPath extracts the import path from a line containing a quoted import.
// Handles plain and aliased imports:
//
//	"fmt"                    → "fmt"
//	"net/http"               → "net/http"
//	json "encoding/json/v2"  → "encoding/json/v2"
//	_ "github.com/lib/pq"   → "github.com/lib/pq"
//	. "fmt"                  → "fmt"
//	"foo" // bar             → "foo"
func extractImportPath(line string) string {
	first := strings.Index(line, "\"")
	if first < 0 {
		return ""
	}
	last := strings.LastIndex(line, "\"")
	if last <= first {
		return ""
	}
	return line[first+1 : last]
}

// parseResult holds the result of parsing a Go source for imports.
type parseResult struct {
	startLine int          // first line of first import (import keyword), -1 if none
	endLine   int          // line after last import block (exclusive)
	groups    importGroups // all imports merged
}

// hasImports returns true if any import declarations were found.
func (r *parseResult) hasImports() bool {
	return r.startLine >= 0
}

// parseSrcImports scans Go source lines, finds all import declarations,
// records the line range from first to last, and collects all import
// paths into a single importGroups.
// Comments and blank lines between import blocks are dropped.
func parseSrcImports(lines []string) parseResult {
	r := parseResult{startLine: -1, endLine: -1}

	i := 0
	for i < len(lines) {
		line := lines[i]

		// Grouped import: import (
		if line == "import (" {
			if r.startLine < 0 {
				r.startLine = i
			}
			i++ // skip "import ("
			for i < len(lines) && lines[i] != ")" {
				trimmed := strings.TrimSpace(lines[i])
				if trimmed != "" {
					path := extractImportPath(trimmed)
					if path != "" {
						r.groups.add(trimmed, path)
					}
				}
				i++
			}
			i++ // skip ")"
			r.endLine = i
			continue
		}

		// Single import: import "foo", import alias "foo"
		if strings.HasPrefix(line, "import ") || strings.HasPrefix(line, "import\t") {
			path := extractImportPath(line)
			if path != "" {
				if r.startLine < 0 {
					r.startLine = i
				}
				r.endLine = i + 1
				spec := strings.TrimSpace(line[len("import"):])
				r.groups.add(spec, path)
			}
			i++
			continue
		}

		i++
	}

	return r
}
