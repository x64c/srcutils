package main

import (
	"path"
	"path/filepath"
	"strings"
)

// patternSet matches slash-separated relative paths against a set of patterns.
//
// Semantics (minimal):
//   - bare name (go.mod)     → matches that basename at any depth
//   - leading / (/README.md) → anchored to the root
//   - * globs within one path segment (path.Match per segment); no **
//
// A pattern that matches a directory protects its entire subtree, which
// protects() enforces by testing every ancestor of the candidate path.
type patternSet struct {
	patterns []string
}

// protects reports whether rel, or any of its ancestor directories, matches.
func (ps patternSet) protects(rel string) bool {
	rel = filepath.ToSlash(rel)
	for {
		for _, p := range ps.patterns {
			if matchPattern(p, rel) {
				return true
			}
		}
		i := strings.LastIndex(rel, "/")
		if i < 0 {
			return false
		}
		rel = rel[:i]
	}
}

func matchPattern(pattern, rel string) bool {
	if strings.HasPrefix(pattern, "/") {
		return matchSegments(strings.Split(strings.TrimPrefix(pattern, "/"), "/"), strings.Split(rel, "/"))
	}
	pat := strings.Split(pattern, "/")
	seg := strings.Split(rel, "/")
	if len(pat) > len(seg) {
		return false
	}
	return matchSegments(pat, seg[len(seg)-len(pat):])
}

func matchSegments(pat, seg []string) bool {
	if len(pat) != len(seg) {
		return false
	}
	for i := range pat {
		ok, err := path.Match(pat[i], seg[i])
		if err != nil || !ok {
			return false
		}
	}
	return true
}
