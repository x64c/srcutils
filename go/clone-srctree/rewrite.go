package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// rwPair is a single import-path rewrite. The quote is baked into old/new so a
// plain byte replacement only touches quoted import prefixes.
type rwPair struct {
	label string // importmap key, for messages
	old   []byte
	new   []byte
}

// importPairs derives .go rewrites from the importmap: for each key K -> to T,
// both the sub-package form "K/ -> "T/ and the exact form "K" -> "T". Keys are
// applied longest-first so a longer key is never clobbered by a shorter prefix.
func importPairs(im map[string]importEntry) []rwPair {
	keys := make([]string, 0, len(im))
	for k := range im {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) > len(keys[j])
		}
		return keys[i] < keys[j]
	})
	pairs := make([]rwPair, 0, len(keys)*2)
	for _, k := range keys {
		t := im[k].To
		pairs = append(pairs,
			rwPair{label: k, old: []byte("\"" + k + "/"), new: []byte("\"" + t + "/")},
			rwPair{label: k, old: []byte("\"" + k + "\""), new: []byte("\"" + t + "\"")},
		)
	}
	return pairs
}

// rewriteTarget applies rw pairs to every non-protected target .go file.
func rewriteTarget(to string, keeps patternSet, pairs []rwPair, res *result, verbose bool) {
	if len(pairs) == 0 {
		return
	}
	err := filepath.WalkDir(to, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := relOf(to, p)
		if rel == "." {
			return nil
		}
		if keeps.protects(rel) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out := data
		for _, pr := range pairs {
			out = bytes.ReplaceAll(out, pr.old, pr.new)
		}
		if bytes.Equal(out, data) {
			return nil
		}
		info, err := os.Stat(p)
		if err != nil {
			return err
		}
		if err := os.WriteFile(p, out, info.Mode().Perm()); err != nil {
			return err
		}
		res.rewritten = append(res.rewritten, rel)
		if verbose {
			stderr("  rewrite %s\n", rel)
		}
		return nil
	})
	if err != nil {
		die(4, "rewrite: %v\n", err)
	}
	sort.Strings(res.rewritten)
}

// planRewrites reports which files a real run would rewrite, scanning source
// (the post-copy content) rather than the untouched target. Reported paths are
// target-relative (where the rewrite would land).
func planRewrites(from string, keeps, skips patternSet, tm treeMap, pairs []rwPair, res *result) {
	if len(pairs) == 0 {
		return
	}
	err := filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := relOf(from, p)
		if rel == "." {
			return nil
		}
		if skips.protects(rel) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		trel, inDomain := tm.srcToTarget(rel)
		if d.IsDir() {
			if !inDomain {
				if tm.domainAncestor(rel) {
					return nil
				}
				return fs.SkipDir
			}
			if keeps.protects(trel) {
				return fs.SkipDir
			}
			return nil
		}
		if !inDomain || keeps.protects(trel) || !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, pr := range pairs {
			if bytes.Contains(data, pr.old) {
				res.rewritten = append(res.rewritten, trel)
				break
			}
		}
		return nil
	})
	if err != nil {
		die(4, "plan rewrites: %v\n", err)
	}
	sort.Strings(res.rewritten)
}

// verifyResidue scans all target .go files (rewrite excludes protected paths;
// verification deliberately does not) for any surviving "OLD occurrence.
func verifyResidue(to string, pairs []rwPair) []string {
	var hits []string
	if len(pairs) == 0 {
		return nil
	}
	err := filepath.WalkDir(to, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel := relOf(to, p)
		for n, line := range strings.Split(string(data), "\n") {
			lb := []byte(line)
			for _, pr := range pairs {
				if bytes.Contains(lb, pr.old) {
					hits = append(hits, fmt.Sprintf("%s:%d: %s", rel, n+1, strings.TrimSpace(line)))
				}
			}
		}
		return nil
	})
	if err != nil {
		die(4, "verify: %v\n", err)
	}
	sort.Strings(hits)
	return hits
}
