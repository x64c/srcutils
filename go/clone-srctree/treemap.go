package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
)

// treeMap translates paths between the source and target trees.
//
// Pair mode (no importprefix): the identity map — roots are paired and every
// relative path is preserved, so the whole source tree is in domain.
//
// Prefixed mode: each discovered source module dir maps to its importmap
// value (the prefix-relative path doubles as the target dir). Files outside
// every module subtree are out of domain: not copied, and their target-side
// counterparts (minus keeps) count as stale.
type treeMap struct {
	identity bool
	pairs    []dirPair // sorted longest source first
}

type dirPair struct {
	src string // source-relative module dir ("." allowed)
	dst string // target-relative dir
}

// buildTreeMap resolves the source→target geometry. In prefixed mode every
// discovered module must have an importmap entry (or be excluded) — placement
// and the module line both derive from it, so an unmapped module is a conf error.
func buildTreeMap(cfg Config, srcModDirs []string) treeMap {
	if cfg.ImportPrefix == "" {
		return treeMap{identity: true}
	}
	tm := treeMap{}
	for _, dir := range srcModDirs {
		modPath := readModulePath(filepath.Join(cfg.From, filepath.FromSlash(dir), "go.mod"))
		e, ok := cfg.ImportMap[modPath]
		if !ok {
			die(1, "importprefix mode: discovered module %s (at %s) has no importmap entry; map it or add its dir to 'exclude'\n", modPath, dir)
		}
		tm.pairs = append(tm.pairs, dirPair{src: dir, dst: e.Dir})
	}
	sort.Slice(tm.pairs, func(i, j int) bool {
		return len(tm.pairs[i].src) > len(tm.pairs[j].src)
	})
	return tm
}

func readModulePath(gomodPath string) string {
	data, err := os.ReadFile(gomodPath)
	if err != nil {
		die(4, "read %s: %v\n", gomodPath, err)
	}
	f, err := modfile.ParseLax(gomodPath, data, nil)
	if err != nil {
		die(4, "parse %s: %v\n", gomodPath, err)
	}
	if f.Module == nil {
		die(1, "%s: no module line\n", gomodPath)
	}
	return f.Module.Mod.Path
}

// srcToTarget maps a source-relative path into the target tree.
// ok=false means the path is outside the clone domain (prefixed mode only).
func (tm treeMap) srcToTarget(rel string) (string, bool) {
	if tm.identity {
		return rel, true
	}
	for _, p := range tm.pairs {
		if mapped, ok := remap(rel, p.src, p.dst); ok {
			return mapped, true
		}
	}
	return "", false
}

// targetToSrc maps a target-relative path back into the source tree.
// ok=false means no source location corresponds (the path is stale territory).
func (tm treeMap) targetToSrc(rel string) (string, bool) {
	if tm.identity {
		return rel, true
	}
	for _, p := range tm.pairs {
		if mapped, ok := remap(rel, p.dst, p.src); ok {
			return mapped, true
		}
	}
	return "", false
}

// domainAncestor reports whether the source-relative dir rel is an ancestor of
// (or equal to) some mapped module dir — i.e. the walk must descend through it
// even though the dir itself maps nowhere.
func (tm treeMap) domainAncestor(rel string) bool {
	if tm.identity {
		return true
	}
	for _, p := range tm.pairs {
		if p.src == rel || strings.HasPrefix(p.src, rel+"/") {
			return true
		}
	}
	return false
}

// remap rebases rel from under base onto onto ("." bases mean the tree root).
func remap(rel, base, onto string) (string, bool) {
	if base == "." {
		if onto == "." {
			return rel, true
		}
		return onto + "/" + rel, true
	}
	if rel == base {
		return onto, true
	}
	if strings.HasPrefix(rel, base+"/") {
		if onto == "." {
			return rel[len(base)+1:], true
		}
		return onto + "/" + rel[len(base)+1:], true
	}
	return "", false
}
