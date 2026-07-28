package sortimports

import "sort"

// importGroups holds import lines split into stdlib and non-stdlib groups.
type importGroups struct {
	stdlib []string
	others []string
}

// add appends an import line to the appropriate group.
func (g *importGroups) add(srcLine string, importPath string) {
	if isStdlib(importPath) {
		g.stdlib = append(g.stdlib, srcLine)
	} else {
		g.others = append(g.others, srcLine)
	}
}

// total returns the total number of imports across both groups.
func (g *importGroups) total() int {
	return len(g.stdlib) + len(g.others)
}

// sort sorts each group alphabetically by import path in place.
func (g *importGroups) sort() {
	sortByImportPath(g.stdlib)
	sortByImportPath(g.others)
}

func sortByImportPath(lines []string) {
	sort.Slice(lines, func(i, j int) bool {
		return extractImportPath(lines[i]) < extractImportPath(lines[j])
	})
}

// formatLines returns the sorted import block as source lines.
// Single import → []string{"import \"foo\""}.
// Multiple → []string{"import (", "\t...", ")"}.
func (g *importGroups) formatLines() []string {
	g.sort()

	if g.total() == 1 {
		var imp string
		if len(g.stdlib) == 1 {
			imp = g.stdlib[0]
		} else {
			imp = g.others[0]
		}
		return []string{"import " + imp}
	}

	var out []string
	out = append(out, "import (")
	for _, s := range g.stdlib {
		out = append(out, "\t"+s)
	}
	if len(g.stdlib) > 0 && len(g.others) > 0 {
		out = append(out, "")
	}
	for _, s := range g.others {
		out = append(out, "\t"+s)
	}
	out = append(out, ")")
	return out
}
