package main

import (
	"fmt"
	"strings"
)

// run executes the full sync pipeline against an already-validated config.
func run(cfg Config) {
	keeps := patternSet{cfg.keepPatterns()}
	skips := patternSet{cfg.skipPatterns()}
	pairs := importPairs(cfg.ImportMap)
	genActive := cfg.ImportMap != nil

	// 0. geometry: discover source modules, resolve the source→target tree map
	// (identity in pair mode; importmap-value dirs in prefixed mode)
	var srcMods []string
	if genActive {
		srcMods = discoverSourceModules(cfg.From, skips)
	}
	tm := buildTreeMap(cfg, srcMods)
	gwPlan := planGoWork(cfg, keeps, skips, tm)

	var res result

	// 1. delete stale, then prune emptied dirs
	deleteStale(cfg.From, cfg.To, keeps, tm, gwPlan, &res, cfg.Dry, cfg.Verbose)
	removeEmptyDirs(cfg.From, cfg.To, keeps, tm, &res, cfg.Dry, cfg.Verbose)

	// 2. copy (raw go.mod/go.sum excluded when generation applies; ditto
	// go.work/go.work.sum when the workspace plan owns them)
	copyTree(cfg.From, cfg.To, keeps, skips, genActive, tm, gwPlan, &res, cfg.Dry, cfg.Verbose)

	// 3. rewrite (dry scans source, i.e. post-copy content)
	if cfg.Dry {
		planRewrites(cfg.From, keeps, skips, tm, pairs, &res)
	} else {
		rewriteTarget(cfg.To, keeps, pairs, &res, cfg.Verbose)
	}

	// 4. verify (real runs only)
	if !cfg.Dry {
		if hits := verifyResidue(cfg.To, pairs); len(hits) > 0 {
			stderr("VERIFY FAILED: rewrite residue in %d location(s):\n", len(hits))
			for _, h := range hits {
				stderr("  %s\n", h)
			}
			die(2, "aborting: import-path rewrite left residue\n")
		}
	}

	// 5. generate go.mod(s) + resolve pins; tidy only on module-alone runs
	// (tidy needs published pins — see generateStep); plan only (dry)
	generateStep(cfg, keeps, tm, srcMods, &res, cfg.Dry)

	// 5b. go.work: generated when the source is a workspace and the target's
	// isn't kept — clone content, independent of any buildtest. Bridges are
	// emitted only for CONSUMED modules, read off the pins step 5 resolved.
	consumed := map[string]bool{}
	for _, g := range res.gomods {
		for _, pin := range g.pins {
			if i := strings.IndexByte(pin, ' '); i > 0 {
				consumed[pin[:i]] = true
			}
		}
	}
	goworkStatus := goworkStep(cfg, tm, gwPlan, consumed, cfg.Dry)

	// 6. sort-imports, 7. build (always after the WHOLE clone)
	siStatus := sortStep(cfg.To, cfg.Dry)
	buildStatus := buildTestStep(cfg, tm, srcMods, keeps, gwPlan, cfg.Dry)

	if cfg.Dry {
		printDry(&res, len(pairs), genActive, cfg.Verbose, goworkStatus, siStatus, buildStatus)
	} else {
		printSummary(&res, goworkStatus, siStatus, buildStatus)
	}
}

func printList(header string, items []string) {
	fmt.Printf("\n%s (%d):\n", header, len(items))
	for _, s := range items {
		fmt.Printf("  %s\n", s)
	}
}

func gomodLine(g gomodResult) string {
	pins := "none"
	if len(g.pins) > 0 {
		pins = strings.Join(g.pins, ", ")
	}
	return fmt.Sprintf("%s: module %s, pins: %s, replaces: +%d/-%d",
		g.dir, g.module, pins, g.addReplace, g.dropReplace)
}

func printDry(res *result, nPairs int, genActive, verbose bool, goworkStatus, siStatus, buildStatus string) {
	fmt.Println("=== DRY RUN (nothing changed) ===")

	fmt.Printf("\nDELETIONS (%d files, %d dirs):\n", len(res.deletedFiles), len(res.deletedDirs))
	for _, f := range res.deletedFiles {
		fmt.Printf("  - %s\n", f)
	}
	for _, d := range res.deletedDirs {
		fmt.Printf("  - %s/  (empty dir)\n", d)
	}

	printList("NEW FILES", res.newFiles)
	printList("OVERWRITES", res.overwritten)

	fmt.Printf("\nREWRITES: %d pair(s), %d file(s) would change:\n", nPairs, len(res.rewritten))
	for _, f := range res.rewritten {
		fmt.Printf("  %s\n", f)
	}

	if !genActive {
		fmt.Printf("\nGO.MOD: no importmap (go.mod/go.sum copied verbatim)\n")
	} else {
		fmt.Printf("\nGO.MOD GENERATION (%d module(s)):\n", len(res.gomods))
		for _, g := range res.gomods {
			fmt.Printf("  %s  [%s]\n", gomodLine(g), g.diff)
			if verbose && g.content != nil {
				fmt.Printf("  --- generated go.mod (%s) ---\n%s  --- end ---\n", g.dir, g.content)
			}
		}
	}

	fmt.Printf("\ngowork:       %s\n", goworkStatus)
	fmt.Printf("sort-imports: %s\n", siStatus)
	fmt.Printf("buildtest:    %s\n", buildStatus)
}

func printSummary(res *result, goworkStatus, siStatus, buildStatus string) {
	fmt.Println(strings.Repeat("=", 12) + " summary " + strings.Repeat("=", 12))
	fmt.Printf("deleted:      %d files, %d dirs\n", len(res.deletedFiles), len(res.deletedDirs))
	fmt.Printf("copied (new): %d\n", len(res.newFiles))
	fmt.Printf("overwritten:  %d\n", len(res.overwritten))
	fmt.Printf("rewritten:    %d\n", len(res.rewritten))
	for _, g := range res.gomods {
		fmt.Printf("gomod %s\n", gomodLine(g))
	}
	fmt.Printf("gowork:       %s\n", goworkStatus)
	fmt.Printf("sort-imports: %s\n", siStatus)
	fmt.Printf("buildtest:    %s\n", buildStatus)
}
