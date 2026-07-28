package main

import (
	"flag"
	"os"

	"github.com/x64c/srcutils/go/internal/toolver"
)

func usage() {
	stderr("publish-family — wave-ordered publisher for a module FAMILY: many modules,\n")
	stderr("ONE version, living under one family root in one umbrella git repo.\n\n")
	stderr("usage:\n")
	stderr("  publish-family -conf <file> [-version vX.Y.Z] [-dry]\n\n")
	stderr("The conf declares STRUCTURE only: repo, familyroot, modprefix, commitmsg,\n")
	stderr("goenv, and the EXPLICIT module dependency graph (dir -> family deps).\n")
	stderr("familyroot (REQUIRED-explicit; \"\" = repo root) is the family's root path\n")
	stderr("WITHIN the repo: a repo may host several independent families, so every\n")
	stderr("commit the run makes — commit #0, wave commits, the closing sweep — is\n")
	stderr("fenced to it; dirt outside is reported, never staged. Pushes stay branch-\n")
	stderr("level (git has no subtree push): already-committed work rides along, which\n")
	stderr("is safe BECAUSE every family's commits are fenced. Everything per-run is\n")
	stderr("read from the world, never from tool state:\n")
	stderr("  version — inferred from the modules' unanimous 'hoping requires' (the\n")
	stderr("            family pins a sync wrote ahead of their tags); -version asserts.\n")
	stderr("  waves   — topological stages of the conf graph, cross-validated against\n")
	stderr("            each go.mod's actual family requires (must match exactly).\n\n")
	stderr("Each module's pub cycle is the step sequence\n")
	stderr("    go mod tidy -> commit -> tag -> push tag -> nudge -> wait LANDED\n")
	stderr("entered at the step the PRECHECK finds undone, probing the deepest published\n")
	stderr("state backward: remote tag? (one ls-remote/run; only then ask the proxy —\n")
	stderr("probing an unpushed version plants a negative-cached 404) -> local tag? ->\n")
	stderr("module dir clean? -> else fresh from tidy. Already-landed modules cost\n")
	stderr("nothing; a killed run, re-run, continues at the first undone step.\n\n")
	stderr("Run shape: commit #0 (pre-existing non-module dirt, e.g. go.work) -> per\n")
	stderr("wave: tidy+build each fresh module GOWORK=off (legal: deps landed by\n")
	stderr("construction), ONE commit of the wave's dirs in FINAL form, module-path-\n")
	stderr("relative tags, one push (commit+tags), pause ~15s, nudge, poll until LANDED\n")
	stderr("-> closing sweep: leftovers committed untagged so remote equals tree.\n\n")
	stderr("Exit codes: 0 ok, 1 usage/config, 2 preflight, 3 tidy/build, 4 git/I-O,\n")
	stderr("5 landing timeout (safe to re-run).\n")
}

func main() {
	flag.Usage = usage
	conf := flag.String("conf", "", "JSON config file (required)")
	assertVersion := flag.String("version", "", "assert the inferred family version (optional)")
	dry := flag.Bool("dry", false, "preflight + plan only; no tidy, no git writes, no pushes")
	version := flag.Bool("v", false, "print version and exit")
	flag.Parse()

	if *version {
		toolver.Print()
		return
	}
	if *conf == "" || flag.NArg() > 0 {
		usage()
		os.Exit(1)
	}

	cfg := loadConfig(*conf)
	for k, v := range cfg.GoEnv {
		if err := os.Setenv(k, v); err != nil {
			die(1, "setenv %s: %v\n", k, err)
		}
	}

	p := preflight(cfg, *assertVersion)
	p.print(cfg)
	if *dry {
		stderr("\n=== DRY RUN (nothing changed) ===\n")
		return
	}
	run(cfg, p)
}
