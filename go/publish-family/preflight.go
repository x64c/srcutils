package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

// entry is where a module ENTERS its pub cycle, found by the reverse-order
// precheck: probe from the deepest published state backward; the first "yes"
// wins. Each state is exactly one crash window of the step sequence
// tidy -> commit -> tag -> push -> nudge -> wait.
//
//	landed on proxy?           yes -> entryDone  (nothing to do)
//	  (asked ONLY when the remote tag exists — no remote tag means the proxy
//	   CANNOT know the version, and probing it anyway plants a negative-cached
//	   404 that outlives the honest nudges after the push)
//	tag on remote?             yes -> entryAwait (nudge + wait)
//	tag local only?            yes -> entryPush  (push tag, then await)
//	module dir clean?          yes -> entryTag   (commit exists; tag onward)
//	otherwise                       -> entryFresh (full cycle from tidy)
type entry int

const (
	entryDone entry = iota
	entryAwait
	entryPush
	entryTag
	entryFresh
)

func (e entry) String() string {
	return [...]string{"DONE", "await", "push tag", "tag", "fresh"}[e]
}

// modState is one module's precheck verdict.
type modState struct {
	dir   string
	tag   string // <dir>/<version>
	entry entry
}

// plan is the settled preview of the whole run, derived from the conf + the
// repo + (for pushed tags only) the proxy — never from tool-local state.
type plan struct {
	version  string
	waves    [][]string
	states   map[string]*modState
	rootDirt []string // dirty paths owned by no module: commit #0 material
}

// preflight reads the world and validates it against the conf's instruction.
//
// The family version is INFERRED: after a family sync every consumer go.mod
// carries the same "hoping require" for its family deps (a version that may
// not be published yet — that's the point). All hoping requires must be
// unanimous; -version (assertVersion) only asserts the inferred value.
// The conf's explicit dependency lists must exactly match each go.mod's
// actual family requires — instruction and reality agree, or nothing runs.
func preflight(cfg Config, assertVersion string) plan {
	p := plan{states: map[string]*modState{}}

	version := ""
	versionFrom := ""
	for dir := range cfg.Modules {
		gomodPath := filepath.Join(cfg.Repo, filepath.FromSlash(dir), "go.mod")
		data, err := os.ReadFile(gomodPath)
		if err != nil {
			die(4, "read %s: %v\n", gomodPath, err)
		}
		mf, err := modfile.Parse(gomodPath, data, nil)
		if err != nil {
			die(2, "parse %s: %v\n", gomodPath, err)
		}
		if mf.Module == nil || mf.Module.Mod.Path != cfg.modPath(dir) {
			die(2, "module %s: go.mod declares %q, conf implies %q\n",
				dir, mf.Module.Mod.Path, cfg.modPath(dir))
		}
		famReqs := map[string]string{} // dep dir -> required version
		for _, r := range mf.Require {
			if !strings.HasPrefix(r.Mod.Path, cfg.ModPrefix) {
				continue
			}
			famReqs[strings.TrimPrefix(r.Mod.Path, cfg.ModPrefix)] = r.Mod.Version
		}
		// instruction vs reality, both directions
		for _, d := range cfg.Modules[dir] {
			v, ok := famReqs[d]
			if !ok {
				die(2, "module %s: conf declares dep %q but go.mod has no such family require\n", dir, d)
			}
			if version == "" {
				version, versionFrom = v, dir+" -> "+d
			} else if v != version {
				die(2, "family not coherently bumped: %s requires %s@%s, but %s says %s — resync\n",
					dir, d, v, versionFrom, version)
			}
			delete(famReqs, d)
		}
		if len(famReqs) > 0 {
			var extra []string
			for d := range famReqs {
				extra = append(extra, d)
			}
			sort.Strings(extra)
			die(2, "module %s: go.mod requires family module(s) the conf does not declare: %s\n",
				dir, strings.Join(extra, ", "))
		}
	}
	if version == "" {
		if assertVersion == "" {
			die(2, "no family-internal requires exist — version cannot be inferred; pass -version\n")
		}
		version = assertVersion
	}
	if !semver.IsValid(version) {
		die(2, "inferred version %q is not valid semver\n", version)
	}
	if assertVersion != "" && assertVersion != version {
		die(2, "-version %s does not match the version the repo is bumped to (%s)\n", assertVersion, version)
	}
	p.version = version
	p.waves = cfg.waves()

	// dirty inventory, partitioned by ownership
	dirtyByDir := map[string]bool{}
	for _, rel := range gitDirty(cfg.Repo) {
		if owner := cfg.ownedBy(rel); owner != "" {
			dirtyByDir[owner] = true
		} else {
			p.rootDirt = append(p.rootDirt, rel)
		}
	}
	sort.Strings(p.rootDirt)

	// reverse-order precheck per module (see the entry ladder above)
	remoteTags := gitRemoteTags(cfg.Repo)
	for dir := range cfg.Modules {
		st := &modState{dir: dir, tag: dir + "/" + version}
		switch {
		case remoteTags[st.tag]:
			if proxyLanded(cfg.modPath(dir), version) {
				st.entry = entryDone
				if dirtyByDir[dir] {
					die(2, "module %s: %s is LANDED on the proxy but the dir has uncommitted changes — a landed version is immutable; resolve by hand\n",
						dir, st.tag)
				}
			} else {
				st.entry = entryAwait
			}
		case gitTagExists(cfg.Repo, st.tag):
			st.entry = entryPush
		case !dirtyByDir[dir]:
			st.entry = entryTag
		default:
			st.entry = entryFresh
		}
		p.states[dir] = st
	}
	return p
}

func (p plan) print(cfg Config) {
	stderr("family version: %s (inferred from hoping requires)\n", p.version)
	stderr("waves (each module enters at its prechecked step):\n")
	for i, wave := range p.waves {
		stderr("  %d:", i+1)
		for _, dir := range wave {
			stderr("  %s [%s]", dir, p.states[dir].entry)
		}
		stderr("\n")
	}
	if len(p.rootDirt) > 0 {
		stderr("commit #0 (non-module dirt):\n")
		for _, f := range p.rootDirt {
			stderr("  %s\n", f)
		}
	} else {
		stderr("commit #0: nothing (no non-module dirt)\n")
	}
}
