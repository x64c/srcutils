package main

import (
	"bytes"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
)

// goworkPlan captures the workspace shape of the run, settled before the
// pipeline touches anything. Two interweaved concepts, kept apart:
// go.work GENERATION is clone content (driven by the source's shape and the
// keeps); the workspace BUILDTEST is a question asked afterwards (build.go).
//
// Source go.work count:
//
//	0    — no generation ever; a kept target-only go.work survives, an unkept
//	       one is stale; buildtest "workspace" is unaskable (error).
//	1    — the workspace lives at srcRel; the target counterpart lives at the
//	       corresponding location tgtRel. Kept there → hand-maintained, no
//	       generation (a kept go.work anywhere ELSE = mismatch, hard error).
//	       Not kept → generated at tgtRel. Both buildtest modes askable.
//	many — the tree is mid-surgery (e.g. an incomplete merge): cloning only,
//	       verbatim; ANY buildtest = error — no verdict on it is trustworthy.
type goworkPlan struct {
	srcRels  []string // every go.work in the source tree, source-relative
	tgtRel   string   // single-workspace case: target-relative location
	generate bool     // single case, not kept: we own the target go.work
	kept     bool     // single case, kept at tgtRel: hand-maintained
}

func (p goworkPlan) count() int { return len(p.srcRels) }

// planGoWork discovers source go.work files, resolves the target location and
// the keep interplay, and runs the fail-fast validations (kept-path mismatch;
// buildtest askability lives in build.go against this plan).
func planGoWork(cfg Config, keeps, skips patternSet, tm treeMap) goworkPlan {
	var plan goworkPlan
	_ = filepath.WalkDir(cfg.From, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := relOf(cfg.From, p)
		if d.IsDir() {
			if rel != "." && skips.protects(rel) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() == "go.work" {
			plan.srcRels = append(plan.srcRels, rel)
		}
		return nil
	})
	sort.Strings(plan.srcRels)

	if plan.count() != 1 {
		return plan
	}

	srcRel := plan.srcRels[0]
	// Corresponding location: mapped when the go.work sits inside a module
	// subtree, else the same relative path (dirs outside modules keep their
	// names — only module dirs are renamed by the geometry).
	if t, ok := tm.srcToTarget(srcRel); ok {
		plan.tgtRel = t
	} else {
		plan.tgtRel = srcRel
	}
	plan.kept = keeps.protects(plan.tgtRel)
	plan.generate = !plan.kept && cfg.ImportMap != nil

	// A kept go.work anywhere other than the corresponding location breaks
	// the source↔target workspace correspondence: refuse loudly. (Unkept
	// strays are ordinary stale files; delete-stale handles them.)
	_ = filepath.WalkDir(cfg.To, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := relOf(cfg.To, p)
		if d.IsDir() {
			return nil
		}
		if d.Name() == "go.work" && rel != plan.tgtRel && keeps.protects(rel) {
			die(1, "go.work mismatch: source workspace is %s (target %s), but target keeps a go.work at %s\n",
				srcRel, plan.tgtRel, rel)
		}
		return nil
	})
	return plan
}

// goworkOwned reports whether the plan owns the target-relative path rel as
// workspace machinery: the generated go.work and its go.work.sum (which
// follows its sibling go.work's fate — never copied, never deleted
// independently, refreshed by workspace-mode builds).
func (p goworkPlan) goworkOwned(rel string) bool {
	if !p.generate && !p.kept {
		return false
	}
	dir := path.Dir(p.tgtRel)
	return rel == p.tgtRel || rel == path.Join(dir, "go.work.sum")
}

// generateGoWork builds the target go.work from the source one: use dirs are
// mapped through the tree geometry, entries whose module falls outside the
// clone domain (excluded) are dropped, replaces targeting mapped modules are
// dropped, the go line is carried. Returns the content and a dry-run diff tag.
//
// Family confs (importprefix + version) additionally get a version-bridge
// replace per CONSUMED workspace module — one whose path some generated
// go.mod pins — a bare directive for one, a parenthesized block for more
// (single-imports rule):
//
//	replace <module> <version> => ./<dir>
//
// A `use` directive supplies a module's CONTENT but does not satisfy the
// version REQUIREMENT another workspace module's go.mod declares — on a
// family version bump those pins point at a not-yet-published tag, and the
// module graph fails to load for the whole workspace. The bridge redirects
// the pinned version to the local dir, so a workspace buildtest can verify
// the family BEFORE its tags exist. Inert once the tags are published.
// Unconsumed modules get NO bridge: no requirement edge exists for one to
// redirect, and the dead directive draws "unused replace" warnings in IDEs
// (whose quick-fix removal would then fight the generator forever).
func generateGoWork(cfg Config, tm treeMap, plan goworkPlan, consumed map[string]bool) ([]byte, string) {
	srcPath := filepath.Join(cfg.From, filepath.FromSlash(plan.srcRels[0]))
	data, err := os.ReadFile(srcPath)
	if err != nil {
		die(4, "read %s: %v\n", srcPath, err)
	}
	wf, err := modfile.ParseWork(srcPath, data, nil)
	if err != nil {
		die(4, "parse %s: %v\n", srcPath, err)
	}

	srcDir := path.Dir(plan.srcRels[0])
	tgtDir := path.Dir(plan.tgtRel)
	var uses []string
	var bridges []string // family mode: "<module> <version> => ./<dir>" per workspace module
	for _, u := range wf.Use {
		srcModRel := path.Join(srcDir, strings.TrimPrefix(u.Path, "./"))
		tgtModRel, ok := tm.srcToTarget(srcModRel)
		if !ok {
			continue // excluded / outside the clone domain: drops out of the workspace
		}
		rel, rerr := filepath.Rel(tgtDir, tgtModRel)
		if rerr != nil {
			die(4, "gowork use %s: %v\n", u.Path, rerr)
		}
		uses = append(uses, "./"+filepath.ToSlash(rel))
		if cfg.ImportPrefix != "" && cfg.Version != "" && consumed[cfg.ImportPrefix+tgtModRel] {
			bridges = append(bridges, cfg.ImportPrefix+tgtModRel+" "+cfg.Version+" => ./"+filepath.ToSlash(rel))
		}
	}
	sort.Strings(uses)
	sort.Strings(bridges)

	toSet := map[string]bool{}
	for _, e := range cfg.ImportMap {
		toSet[e.To] = true
	}

	var b bytes.Buffer
	if wf.Go != nil {
		b.WriteString("go " + wf.Go.Version + "\n")
	}
	if len(uses) > 0 {
		b.WriteString("\nuse (\n")
		for _, u := range uses {
			b.WriteString("\t" + u + "\n")
		}
		b.WriteString(")\n")
	}
	writeReplaces(&b, bridges)
	var carried []string // source replaces that survive: not targeting family modules
	for _, rp := range wf.Replace {
		if _, isKey := cfg.ImportMap[rp.Old.Path]; isKey || toSet[rp.Old.Path] {
			continue // family replace: dropped, same rule as go.mod generation
		}
		line := rp.Old.Path
		if rp.Old.Version != "" {
			line += " " + rp.Old.Version
		}
		line += " => " + rp.New.Path
		if rp.New.Version != "" {
			line += " " + rp.New.Version
		}
		carried = append(carried, line)
	}
	writeReplaces(&b, carried)

	content := b.Bytes()
	existing, rerr := os.ReadFile(filepath.Join(cfg.To, filepath.FromSlash(plan.tgtRel)))
	diff := "new"
	if rerr == nil {
		diff = "differs"
		if bytes.Equal(existing, content) {
			diff = "identical"
		}
	}
	return content, diff
}

// writeReplaces emits replace directives: a single one gets the bare directive
// form, two or more get a parenthesized block — same rule sort-imports applies
// to import declarations.
func writeReplaces(b *bytes.Buffer, lines []string) {
	switch len(lines) {
	case 0:
	case 1:
		b.WriteString("\nreplace " + lines[0] + "\n")
	default:
		b.WriteString("\nreplace (\n")
		for _, l := range lines {
			b.WriteString("\t" + l + "\n")
		}
		b.WriteString(")\n")
	}
}

// goworkStep runs go.work generation (real) or planning (dry) and returns a
// one-line status for the summary.
func goworkStep(cfg Config, tm treeMap, plan goworkPlan, consumed map[string]bool, dry bool) string {
	switch {
	case plan.count() == 0:
		return "none (source has no go.work)"
	case plan.count() > 1:
		return "verbatim mirror only (source has " + strconv.Itoa(plan.count()) + " go.work files)"
	case plan.kept:
		return plan.tgtRel + " kept (hand-maintained)"
	case !plan.generate:
		return plan.tgtRel + " copied verbatim (no importmap)"
	}
	content, diff := generateGoWork(cfg, tm, plan, consumed)
	if dry {
		return plan.tgtRel + " would generate  [" + diff + "]"
	}
	out := filepath.Join(cfg.To, filepath.FromSlash(plan.tgtRel))
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		die(4, "mkdir for %s: %v\n", plan.tgtRel, err)
	}
	if err := os.WriteFile(out, content, 0o644); err != nil {
		die(4, "write %s: %v\n", plan.tgtRel, err)
	}
	return plan.tgtRel + " generated  [" + diff + "]"
}
