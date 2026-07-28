package main

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Config declares STRUCTURE only. Every per-run fact (the family version, the
// waves' readiness, resume state) is read from the world: the repo's go.mods,
// its tags, and the module proxy.
type Config struct {
	Repo       string              `json:"repo"`       // umbrella git repo root (workspace root); REQUIRED, absolute
	FamilyRoot *string             `json:"familyroot"` // REQUIRED-explicit family root path within the repo ("" = repo root); every commit the run makes is fenced to it — other families' files in the same repo are never staged
	ModPrefix  string              `json:"modprefix"`  // module path prefix, must end "/"; <ModPrefix><dir> = module path = tag prefix
	CommitMsg  string              `json:"commitmsg"`  // REQUIRED-explicit commit message for every commit the run makes
	GoEnv      map[string]string   `json:"goenv"`      // toolchain env applied to every go subprocess (e.g. GOEXPERIMENT)
	Modules    map[string][]string `json:"modules"`    // dir -> EXPLICIT family-internal deps (dirs); the instruction, cross-validated against go.mods

	famRoot string // validated *FamilyRoot
}

func loadConfig(confPath string) Config {
	data, err := os.ReadFile(confPath)
	if err != nil {
		die(1, "read conf: %v\n", err)
	}
	var cfg Config
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		die(1, "parse conf: %v\n", err)
	}
	if cfg.Repo == "" || !filepath.IsAbs(cfg.Repo) {
		die(1, "conf: repo must be an absolute path\n")
	}
	if fi, err := os.Stat(filepath.Join(cfg.Repo, ".git")); err != nil || !fi.IsDir() {
		die(1, "conf: repo %s is not a git root (no .git directory)\n", cfg.Repo)
	}
	if !strings.HasSuffix(cfg.ModPrefix, "/") {
		die(1, "conf: modprefix must end with \"/\": %q\n", cfg.ModPrefix)
	}
	if cfg.CommitMsg == "" {
		die(1, "conf: commitmsg must be set explicitly\n")
	}
	if len(cfg.Modules) == 0 {
		die(1, "conf: modules must not be empty\n")
	}
	if cfg.FamilyRoot == nil {
		die(1, "conf: familyroot must be set explicitly (\"\" = repo root)\n")
	}
	cfg.famRoot = *cfg.FamilyRoot
	if cfg.famRoot != "" {
		if cfg.famRoot != path.Clean(cfg.famRoot) || strings.HasPrefix(cfg.famRoot, "/") || strings.HasPrefix(cfg.famRoot, "..") {
			die(1, "conf: familyroot must be a clean repo-relative path: %q\n", cfg.famRoot)
		}
		if fi, err := os.Stat(filepath.Join(cfg.Repo, filepath.FromSlash(cfg.famRoot))); err != nil || !fi.IsDir() {
			die(1, "conf: familyroot %q is not a directory under repo\n", cfg.famRoot)
		}
	}
	for dir, deps := range cfg.Modules {
		if fi, err := os.Stat(filepath.Join(cfg.Repo, filepath.FromSlash(dir), "go.mod")); err != nil || fi.IsDir() {
			die(1, "conf: module %q has no go.mod under repo\n", dir)
		}
		if !cfg.underFamilyRoot(dir) {
			die(1, "conf: module %q lies outside familyroot %q\n", dir, cfg.famRoot)
		}
		for _, d := range deps {
			if _, ok := cfg.Modules[d]; !ok {
				die(1, "conf: module %q depends on %q, which is not a modules key\n", dir, d)
			}
		}
	}
	return cfg
}

// underFamilyRoot reports whether a repo-relative slash path lies inside the
// family root — the boundary every commit this tool makes is fenced to.
func (cfg Config) underFamilyRoot(rel string) bool {
	if cfg.famRoot == "" {
		return true
	}
	return rel == cfg.famRoot || strings.HasPrefix(rel, cfg.famRoot+"/")
}

// famRootPathspec is the git pathspec covering exactly the family root.
func (cfg Config) famRootPathspec() string {
	if cfg.famRoot == "" {
		return "."
	}
	return cfg.famRoot
}

// waves stages the EXPLICIT dependency graph: wave N = every module whose
// declared deps all sit in earlier waves. A leftover means a cycle — hard error.
func (cfg Config) waves() [][]string {
	placed := map[string]bool{}
	var out [][]string
	for len(placed) < len(cfg.Modules) {
		var wave []string
		for dir, deps := range cfg.Modules {
			if placed[dir] {
				continue
			}
			ready := true
			for _, d := range deps {
				if !placed[d] {
					ready = false
					break
				}
			}
			if ready {
				wave = append(wave, dir)
			}
		}
		if len(wave) == 0 {
			var stuck []string
			for dir := range cfg.Modules {
				if !placed[dir] {
					stuck = append(stuck, dir)
				}
			}
			sort.Strings(stuck)
			die(1, "conf: dependency cycle among: %s\n", strings.Join(stuck, ", "))
		}
		sort.Strings(wave)
		for _, dir := range wave {
			placed[dir] = true
		}
		out = append(out, wave)
	}
	return out
}

// modPath returns the module path for a conf module dir.
func (cfg Config) modPath(dir string) string {
	return cfg.ModPrefix + dir
}

// ownedBy returns the conf module dir owning a repo-relative path, or "".
// Longest match wins so nested module layouts partition correctly.
func (cfg Config) ownedBy(rel string) string {
	best := ""
	for dir := range cfg.Modules {
		if rel == dir || strings.HasPrefix(rel, dir+"/") {
			if len(dir) > len(best) {
				best = dir
			}
		}
	}
	return best
}

func fmtDeps(deps []string) string {
	if len(deps) == 0 {
		return "(none)"
	}
	s := append([]string(nil), deps...)
	sort.Strings(s)
	return strings.Join(s, ", ")
}
