package sortimports

import (
	"sort"
	"strings"
)

// importGroups holds import lines split into three groups: stdlib, local
// (non-stdlib whose first path element has no dot — module names like
// "g" or "kvdbs/redis"), and others (domain-rooted paths like "github.com/...").
type importGroups struct {
	stdlib []string
	local  []string
	others []string
}

// add appends an import line to the appropriate group.
func (g *importGroups) add(srcLine string, importPath string) {
	switch {
	case isStdlib(importPath):
		g.stdlib = append(g.stdlib, srcLine)
	case !strings.Contains(strings.SplitN(importPath, "/", 2)[0], "."):
		g.local = append(g.local, srcLine)
	default:
		g.others = append(g.others, srcLine)
	}
}

// groups returns the three groups in output order.
func (g *importGroups) groups() [][]string {
	return [][]string{g.stdlib, g.local, g.others}
}

// total returns the total number of imports across all groups.
func (g *importGroups) total() int {
	n := 0
	for _, grp := range g.groups() {
		n += len(grp)
	}
	return n
}

// sort sorts each group alphabetically by import path in place.
func (g *importGroups) sort() {
	for _, grp := range g.groups() {
		sortByImportPath(grp)
	}
}

func sortByImportPath(lines []string) {
	sort.Slice(lines, func(i, j int) bool {
		return extractImportPath(lines[i]) < extractImportPath(lines[j])
	})
}

// formatLines returns the sorted import block as source lines.
// Single import → []string{"import \"foo\""}.
// Multiple → []string{"import (", "\t...", ")"} with a blank line between groups.
func (g *importGroups) formatLines() []string {
	g.sort()

	if g.total() == 1 {
		for _, grp := range g.groups() {
			if len(grp) == 1 {
				return []string{"import " + grp[0]}
			}
		}
	}

	out := []string{"import ("}
	first := true
	for _, grp := range g.groups() {
		if len(grp) == 0 {
			continue
		}
		if !first {
			out = append(out, "")
		}
		first = false
		for _, s := range grp {
			out = append(out, "\t"+s)
		}
	}
	out = append(out, ")")
	return out
}
