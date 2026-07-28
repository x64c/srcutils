package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"

	"github.com/x64c/srcutils/go/internal/toolver"
)

func usage() {
	stderr("usage: clone-srctree -from <dir> -to <dir> [options]\n\n")
	stderr("Mirror a Go source tree (clone = cp -r + delete-stale) with target-side\n")
	stderr("protections, importmap-driven .go + go.mod rewrites (conf only), residue\n")
	stderr("verification, import-order fixing, and a build check.\n\n")
	stderr("Flags:\n")
	stderr("  -from <dir>      Source tree (required).\n")
	stderr("  -to <dir>       Target tree (required). Must not overlap -from.\n")
	stderr("  -keep <pattern>  Target-side protection; repeatable. Protected paths are never\n")
	stderr("                   overwritten, deleted, or walked into. NO defaults — a\n")
	stderr("                   VCS-controlled target keeps its metadata dir explicitly\n")
	stderr("                   (e.g. /.git). go.sum follows its go.mod.\n")
	stderr("  -skip <pattern>  Source-side exclusion; repeatable. NO defaults — skip a\n")
	stderr("                   source's .git yourself if it has one.\n")
	stderr("  -buildtest <m>   ALSO test-compile the clone (a TEST, not a product; throwaway\n")
	stderr("                   outputs; always runs AFTER the whole clone; never changes\n")
	stderr("                   what the clone wrote). The conf declares ONE question:\n")
	stderr("                     module    — self-build, GOWORK=off pinned: the generated\n")
	stderr("                                 modules vs the PUBLISHED world\n")
	stderr("                     workspace — GOWORK pinned to the target go.work at the\n")
	stderr("                                 corresponding location: family coherence under\n")
	stderr("                                 ITS OWN workspace (source must have a go.work)\n")
	stderr("                   Absent = no buildtest. The verdict names the mode used.\n")
	stderr("  -conf <file>     JSON config (keys: from, to, keep, skip, exclude, version,\n")
	stderr("                   importprefix, goenv, buildtest, importmap). importmap/version/\n")
	stderr("                   importprefix/exclude/goenv are conf-only; flags override.\n")
	stderr("                   goenv = env vars the cloned family's toolchain needs (e.g.\n")
	stderr("                   a GOEXPERIMENT); applied to every go subprocess this run.\n")
	stderr("  -dry             Print the plan; change nothing.\n")
	stderr("  -v               Print version and exit.\n")
	stderr("  -vv              Verbose per-file logging.\n\n")
	stderr("Schema v3. importmap values are the mapped module paths (string form), with an\n")
	stderr("object form {\"to\":...,\"replace\":<path>} when a replace rides along. Versions\n")
	stderr("live in ONE root-level \"version\" (vX.Y.Z or @latest, written out): it pins\n")
	stderr("every consumed entry — an entry for a module discovered under -from is an\n")
	stderr("identity mapping and never takes a version (structurally: there is no field).\n")
	stderr("Rewrites: \"K/ -> \"T/ and \"K\" -> \"T in target .go files; each non-kept go.mod\n")
	stderr("is regenerated (modfile). go mod tidy runs per module ONLY on buildtest=module\n")
	stderr("runs — tidy is module-alone (workspace-blind) and needs the pinned versions\n")
	stderr("PUBLISHED; workspace runs are the pre-publish world and skip it.\n\n")
	stderr("importprefix (conf-only) switches to PREFIXED mode: values become prefix-\n")
	stderr("relative module paths AND each discovered module's target dir under -to\n")
	stderr("(so one conf can mirror a whole family into a monorepo root). Every\n")
	stderr("discovered module must be mapped or listed in exclude (from-relative dirs).\n")
	stderr("Files outside the module subtrees are out of the clone domain; target-root\n")
	stderr("extras (README, ...) must be in keep or they are stale.\n\n")
	stderr("go.work is clone CONTENT, independent of buildtest: source has exactly one\n")
	stderr("-> the target counterpart is GENERATED at the corresponding location (use\n")
	stderr("dirs mapped, excluded modules dropped) unless kept there (kept elsewhere =\n")
	stderr("mismatch error); go.work.sum follows its go.work. Source has none -> no\n")
	stderr("generation, buildtest workspace errors. Source has many -> cloning only,\n")
	stderr("verbatim; ANY buildtest errors (mid-merge tree, no verdict trustworthy).\n\n")
	stderr("Pattern semantics: bare name matches that basename at any depth; leading /\n")
	stderr("anchors to the root; * globs within one path segment (no **). A directory\n")
	stderr("match protects its whole subtree.\n\n")
	stderr("Exit codes: 0 ok, 1 usage/config, 2 verify residue, 3 buildtest/tidy, 4 I/O.\n")
}

func main() {
	flag.Usage = usage

	from := flag.String("from", "", "source tree")
	to := flag.String("to", "", "target tree")
	buildtest := flag.String("buildtest", "", "test-compile mode: module|workspace (absent = skip)")
	conf := flag.String("conf", "", "JSON config file")
	dry := flag.Bool("dry", false, "plan only")
	version := flag.Bool("v", false, "print version and exit")
	verbose := flag.Bool("vv", false, "verbose")

	var keep, skip stringList
	flag.Var(&keep, "keep", "target-side protection (repeatable)")
	flag.Var(&skip, "skip", "source-side exclusion (repeatable)")

	flag.Parse()

	if *version {
		toolver.Print()
		os.Exit(0)
	}

	seen := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { seen[f.Name] = true })

	cfg := Config{}
	if *conf != "" {
		if err := loadConf(*conf, &cfg); err != nil {
			die(1, "conf: %v\n", err)
		}
	}

	if seen["from"] {
		cfg.From = *from
	}
	if seen["to"] {
		cfg.To = *to
	}
	if seen["keep"] {
		cfg.Keep = keep
	}
	if seen["skip"] {
		cfg.Skip = skip
	}
	if seen["buildtest"] {
		cfg.BuildTest = *buildtest
	}
	cfg.Dry = *dry
	cfg.Verbose = *verbose

	validate(&cfg)
	// goenv: the cloned family's toolchain requirements, applied to this
	// process so every go subprocess (tidy, buildtest, go list) inherits them.
	// Set before initSortImports — its `go list std` must see them too.
	for k, v := range cfg.GoEnv {
		if err := os.Setenv(k, v); err != nil {
			die(1, "goenv %s: %v\n", k, err)
		}
	}
	initSortImports()
	run(cfg)
}

// validate checks the resolved config and canonicalizes -from/-to.
func validate(cfg *Config) {
	if cfg.From == "" || cfg.To == "" {
		usage()
		die(1, "\nerror: -from and -to are required\n")
	}

	fa, err := filepath.Abs(cfg.From)
	if err != nil {
		die(1, "error: -from: %v\n", err)
	}
	ta, err := filepath.Abs(cfg.To)
	if err != nil {
		die(1, "error: -to: %v\n", err)
	}
	cfg.From, cfg.To = fa, ta

	mustDir(fa, "-from")
	mustDir(ta, "-to")

	if within(ta, fa) || within(fa, ta) {
		die(1, "error: -from and -to must not be nested within each other\n")
	}

	for _, e := range cfg.Exclude {
		p := filepath.Join(fa, filepath.FromSlash(strings.Trim(e, "/")))
		st, err := os.Stat(p)
		if err != nil || !st.IsDir() {
			die(1, "error: exclude %q: no such directory under from\n", e)
		}
	}
}

func mustDir(path, label string) {
	st, err := os.Stat(path)
	if err != nil {
		die(1, "error: %s %s: %v\n", label, path, err)
	}
	if !st.IsDir() {
		die(1, "error: %s %s is not a directory\n", label, path)
	}
}

// within reports whether a is inside or equal to b.
func within(a, b string) bool {
	rel, err := filepath.Rel(b, a)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
