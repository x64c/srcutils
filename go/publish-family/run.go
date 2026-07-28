package main

import (
	"path/filepath"
	"strings"
)

// run executes the release: commit #0 (pre-existing non-module dirt), then the
// waves, then the closing sweep. Within a wave each module runs the step
// sequence tidy -> commit -> tag -> push -> nudge -> wait FORWARD from its
// prechecked entry point; commits happen only here, and each module's files
// are committed exactly once, by its own wave, post-tidy, in final form.
func run(cfg Config, p plan) {
	pending := false
	for _, st := range p.states {
		if st.entry != entryDone {
			pending = true
		}
	}
	if !pending && len(p.rootDirt) == 0 && len(gitDirty(cfg.Repo)) == 0 {
		stderr("family %s fully published and tree clean — nothing to do\n", p.version)
		return
	}

	// commit #0: non-module dirt (final at sync time; no wave owns it)
	if len(p.rootDirt) > 0 {
		for _, f := range p.rootDirt {
			git(cfg.Repo, "add", "--", f)
		}
		if gitStagedAny(cfg.Repo) {
			git(cfg.Repo, "commit", "-m", cfg.CommitMsg)
			stderr("commit #0: %d non-module path(s)\n", len(p.rootDirt))
		}
	}

	for i, wave := range p.waves {
		var work []*modState // this wave's modules that enter anywhere before DONE
		for _, dir := range wave {
			if st := p.states[dir]; st.entry != entryDone {
				work = append(work, st)
			}
		}
		if len(work) == 0 {
			stderr("wave %d: all DONE — skip\n", i+1)
			continue
		}
		var names []string
		for _, st := range work {
			names = append(names, st.dir+" ["+st.entry.String()+"]")
		}
		stderr("wave %d: %s\n", i+1, strings.Join(names, ", "))

		// step: tidy + build (entryFresh only) — go.mods take final form here
		for _, st := range work {
			if st.entry < entryFresh {
				continue
			}
			abs := filepath.Join(cfg.Repo, filepath.FromSlash(st.dir))
			stderr("  tidy  %s\n", st.dir)
			goModuleAlone(abs, "mod", "tidy")
			stderr("  build %s\n", st.dir)
			buildModuleAlone(abs)
			git(cfg.Repo, "add", "--", st.dir)
		}
		// step: one commit for the wave's freshly-finalized modules
		if gitStagedAny(cfg.Repo) {
			git(cfg.Repo, "commit", "-m", cfg.CommitMsg)
			stderr("  commit\n")
		}
		// step: tag (entryTag and deeper)
		for _, st := range work {
			if st.entry >= entryTag {
				git(cfg.Repo, "tag", st.tag)
				stderr("  tag   %s\n", st.tag)
			}
		}
		// step: one push — the wave's commit plus every tag not yet on the remote
		pushArgs := []string{"push", "origin", "HEAD"}
		for _, st := range work {
			if st.entry >= entryPush {
				pushArgs = append(pushArgs, st.tag)
			}
		}
		git(cfg.Repo, pushArgs...)
		stderr("  push  (%d tag(s))\n", len(pushArgs)-3)

		// step: nudge + wait until every module of the wave is landed
		var modpaths []string
		for _, st := range work {
			modpaths = append(modpaths, cfg.modPath(st.dir))
		}
		stderr("  awaiting proxy...\n")
		awaitLanded(modpaths, p.version)
		for _, st := range work {
			st.entry = entryDone
		}
	}

	// closing sweep: dirt created during the run — committed untagged so the
	// remote equals the working tree (clone-as-is). Loud when it touches a
	// module already tagged this run: main is running ahead of that tag.
	if leftover := gitDirty(cfg.Repo); len(leftover) > 0 {
		for _, f := range leftover {
			if owner := cfg.ownedBy(f); owner != "" {
				stderr("WARNING: %s modified after %s was tagged — committing untagged (main runs ahead of the tag)\n",
					owner, p.states[owner].tag)
			}
		}
		git(cfg.Repo, "add", "-A")
		if gitStagedAny(cfg.Repo) {
			git(cfg.Repo, "commit", "-m", cfg.CommitMsg)
			git(cfg.Repo, "push", "origin", "HEAD")
			stderr("closing sweep: %d leftover path(s) committed and pushed, no tags\n", len(leftover))
		}
	}

	// "remote == tree" has TWO failure modes and the dirt sweep covers only
	// one: unpushed commits are the other. A housekeeping-only run commits #0
	// with every wave skipped (no wave push), and a crash between a wave's
	// commit and its push strands the branch — push whenever local is ahead.
	// (Tags re-derive via the precheck ladder; the branch recovers here.)
	if gitAheadOfUpstream(cfg.Repo) {
		git(cfg.Repo, "push", "origin", "HEAD")
		stderr("push: local branch was ahead of origin — synced\n")
	}

	stderr("family %s published: %d wave(s) landed\n", p.version, len(p.waves))
}
