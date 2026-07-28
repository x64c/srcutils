package main

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// result accumulates what a run did (or, in dry mode, would do).
type result struct {
	deletedFiles []string
	deletedDirs  []string
	newFiles     []string
	overwritten  []string
	rewritten    []string
	gomods       []gomodResult
}

func relOf(base, p string) string {
	r, err := filepath.Rel(base, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(r)
}

// isKeptPath reports whether rel is protected from sync. A go.sum inherits its
// sibling go.mod's keep status (go.sum never syncs independently of go.mod).
func isKeptPath(keeps patternSet, rel string) bool {
	if keeps.protects(rel) {
		return true
	}
	if path.Base(rel) == "go.sum" {
		return keeps.protects(path.Join(path.Dir(rel), "go.mod"))
	}
	return false
}

// deleteStale removes target files that have no counterpart file in source and
// are not protected. Nothing is protected by default — a VCS-controlled target
// keeps its metadata dir (e.g. "/.git") explicitly; the -dry deletion list is
// the guard. The counterpart location comes from the treeMap; a target path
// that maps nowhere (outside every mapped module dir, prefixed mode) is stale
// territory.
func deleteStale(from, to string, keeps patternSet, tm treeMap, gwPlan goworkPlan, res *result, dry, verbose bool) {
	err := filepath.WalkDir(to, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := relOf(to, p)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if keeps.protects(rel) {
				return fs.SkipDir
			}
			return nil
		}
		if isKeptPath(keeps, rel) {
			return nil
		}
		if gwPlan.goworkOwned(rel) {
			return nil // workspace machinery: generated/kept go.work + its go.work.sum
		}
		if srel, ok := tm.targetToSrc(rel); ok {
			si, serr := os.Lstat(filepath.Join(from, filepath.FromSlash(srel)))
			if serr == nil && !si.IsDir() {
				return nil // present in source
			}
		}
		res.deletedFiles = append(res.deletedFiles, rel)
		if verbose && !dry {
			stderr("  delete %s\n", rel)
		}
		if !dry {
			if err := os.Remove(p); err != nil {
				die(4, "delete %s: %v\n", rel, err)
			}
		}
		return nil
	})
	if err != nil {
		die(4, "walk target: %v\n", err)
	}
	sort.Strings(res.deletedFiles)
}

// removeEmptyDirs deletes target directories that are unprotected, absent in
// source, and hold no surviving content.
func removeEmptyDirs(from, to string, keeps patternSet, tm treeMap, res *result, dry, verbose bool) {
	var dirs, files []string
	err := filepath.WalkDir(to, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := relOf(to, p)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if keeps.protects(rel) {
				return fs.SkipDir
			}
			dirs = append(dirs, rel)
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		die(4, "walk target: %v\n", err)
	}

	deleted := make(map[string]struct{}, len(res.deletedFiles))
	for _, f := range res.deletedFiles {
		deleted[f] = struct{}{}
	}
	survives := func(dir string) bool {
		prefix := dir + "/"
		for _, f := range files {
			if _, gone := deleted[f]; gone {
				continue
			}
			if strings.HasPrefix(f, prefix) {
				return true
			}
		}
		return false
	}

	sort.Slice(dirs, func(i, j int) bool {
		return strings.Count(dirs[i], "/") > strings.Count(dirs[j], "/")
	})
	for _, rel := range dirs {
		if srel, ok := tm.targetToSrc(rel); ok && sourceHasDir(from, srel) {
			continue
		}
		full := filepath.Join(to, rel)
		if dry {
			if !survives(rel) {
				res.deletedDirs = append(res.deletedDirs, rel)
			}
			continue
		}
		if err := os.Remove(full); err == nil {
			res.deletedDirs = append(res.deletedDirs, rel)
			if verbose {
				stderr("  rmdir %s\n", rel)
			}
		}
	}
	sort.Strings(res.deletedDirs)
}

func sourceHasDir(from, rel string) bool {
	st, err := os.Stat(filepath.Join(from, filepath.FromSlash(rel)))
	return err == nil && st.IsDir()
}

// skipCopyFile reports whether a source file should not be copied (rel is the
// TARGET-relative path): it is keep-protected, (when generation is active) a
// go.mod/go.sum the generation step will produce instead, or go.work machinery
// the workspace plan owns (single-workspace case: generated or kept — never
// raw-copied; go.work.sum follows its go.work). A multi-workspace source has
// no plan ownership, so its go.work files mirror verbatim.
func skipCopyFile(keeps patternSet, rel string, genActive bool, gwPlan goworkPlan) bool {
	if isKeptPath(keeps, rel) {
		return true
	}
	if gwPlan.goworkOwned(rel) {
		return true
	}
	if genActive {
		switch path.Base(rel) {
		case "go.mod", "go.sum":
			return true
		}
	}
	return false
}

// copyTree mirrors every in-domain source file into its mapped target path,
// overwriting, except where the source path is skipped, the target path is
// keep-protected, or the file is handled by go.mod generation. Source paths
// outside the clone domain (prefixed mode) are not copied; dirs are still
// walked while they can lead to a mapped module dir. Symlinks are skipped
// with a warning.
func copyTree(from, to string, keeps, skips patternSet, genActive bool, tm treeMap, gwPlan goworkPlan, res *result, dry, verbose bool) {
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
					return nil // keep walking; nothing to create here
				}
				return fs.SkipDir
			}
			if keeps.protects(trel) {
				return fs.SkipDir
			}
			if !dry {
				mode := fs.FileMode(0o755)
				if info, e := d.Info(); e == nil {
					mode = info.Mode().Perm()
				}
				if err := os.MkdirAll(filepath.Join(to, filepath.FromSlash(trel)), mode); err != nil {
					die(4, "mkdir %s: %v\n", trel, err)
				}
			}
			return nil
		}
		if !inDomain {
			return nil
		}
		if skipCopyFile(keeps, trel, genActive, gwPlan) {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			stderr("warning: skipping symlink %s\n", rel)
			return nil
		}

		target := filepath.Join(to, filepath.FromSlash(trel))
		exists := false
		if _, e := os.Stat(target); e == nil {
			exists = true
		}
		if exists {
			res.overwritten = append(res.overwritten, trel)
		} else {
			res.newFiles = append(res.newFiles, trel)
		}
		if verbose && !dry {
			verb := "new"
			if exists {
				verb = "overwrite"
			}
			stderr("  %s %s\n", verb, trel)
		}
		if !dry {
			copyFile(p, target, trel)
		}
		return nil
	})
	if err != nil {
		die(4, "walk source: %v\n", err)
	}
	sort.Strings(res.newFiles)
	sort.Strings(res.overwritten)
}

func copyFile(src, dst, rel string) {
	info, err := os.Stat(src)
	if err != nil {
		die(4, "stat %s: %v\n", rel, err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		die(4, "read %s: %v\n", rel, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		die(4, "mkdir for %s: %v\n", rel, err)
	}
	if err := os.WriteFile(dst, data, info.Mode().Perm()); err != nil {
		die(4, "write %s: %v\n", rel, err)
	}
	if err := os.Chmod(dst, info.Mode().Perm()); err != nil {
		die(4, "chmod %s: %v\n", rel, err)
	}
}
