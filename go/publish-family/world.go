package main

import (
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/mod/module"
)

// ---- git (all commands run -C repo) ----

func git(repo string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		die(4, "git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// gitQuiet runs git and reports success without dying (for existence probes).
func gitQuiet(repo string, args ...string) (string, bool) {
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

// gitDirty returns repo-relative paths with uncommitted changes (staged,
// unstaged, or untracked).
func gitDirty(repo string) []string {
	var out []string
	for _, line := range strings.Split(git(repo, "status", "--porcelain"), "\n") {
		if len(line) < 4 {
			continue
		}
		p := strings.TrimSpace(line[3:])
		// rename lines are "old -> new": the new path is the live one
		if i := strings.Index(p, " -> "); i >= 0 {
			p = p[i+4:]
		}
		out = append(out, strings.Trim(p, `"`))
	}
	return out
}

func gitTagExists(repo, tag string) bool {
	return strings.TrimSpace(git(repo, "tag", "-l", tag)) == tag
}

// gitRemoteTags lists the remote's tags in ONE round-trip — the truth of
// "pushed". Peeled entries (^{}) collapse onto their tag name.
func gitRemoteTags(repo string) map[string]bool {
	out := map[string]bool{}
	for _, line := range strings.Split(git(repo, "ls-remote", "--tags", "origin"), "\n") {
		_, ref, ok := strings.Cut(line, "\trefs/tags/")
		if !ok {
			continue
		}
		out[strings.TrimSuffix(strings.TrimSpace(ref), "^{}")] = true
	}
	return out
}

// gitStagedAny reports whether the index holds anything to commit.
func gitStagedAny(repo string) bool {
	_, clean := gitQuiet(repo, "diff", "--cached", "--quiet")
	return !clean
}

// gitAheadOfUpstream reports whether the local branch holds commits its
// upstream lacks. No configured upstream reads as not-ahead: there is nothing
// to compare against, and the wave pushes use an explicit origin anyway.
func gitAheadOfUpstream(repo string) bool {
	out, ok := gitQuiet(repo, "rev-list", "--count", "@{upstream}..HEAD")
	if !ok {
		return false
	}
	return strings.TrimSpace(out) != "0"
}

// ---- go subprocesses (module-alone world: GOWORK=off) ----

// goModuleAlone runs `go <args>` inside dir with GOWORK=off — the published
// world. Legal for a module only once its family deps are landed, which the
// wave order guarantees.
func goModuleAlone(dir string, args ...string) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		die(3, "go %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
}

// hasMainPackage reports whether any package under dir is package main.
// (Mirrors clone-srctree/build.go.)
func hasMainPackage(dir string, env []string) bool {
	cmd := exec.Command("go", "list", "-f", "{{.Name}}", "./...")
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return false // let the build itself surface the problem
	}
	for _, name := range strings.Fields(string(out)) {
		if name == "main" {
			return true
		}
	}
	return false
}

// buildModuleAlone test-compiles a module without touching the tree — a TEST,
// never a product (mirrors clone-srctree's buildtest): -o to a throwaway temp
// dir ONLY when mains exist (go refuses -o for main-less patterns, and
// library-only builds cannot leave residue anyway).
func buildModuleAlone(dir string) {
	env := append(os.Environ(), "GOWORK=off")
	args := []string{"build", "./..."}
	if hasMainPackage(dir, env) {
		tmp, err := os.MkdirTemp("", "publish-family-build-")
		if err != nil {
			die(4, "mktemp for build: %v\n", err)
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		args = []string{"build", "-o", tmp, "./..."}
	}
	goModuleAlone(dir, args...)
}

// ---- proxy ----

const (
	proxyBase       = "https://proxy.golang.org/"
	firstNudgeDelay = 15 * time.Second // push→nudge pause: a too-early nudge's 404 is negative-cached for minutes
	pollInterval    = 20 * time.Second
	landTimeout     = 15 * time.Minute
)

// proxyLanded reports whether the proxy serves modpath@version right now.
func proxyLanded(modpath, version string) bool {
	esc, err := module.EscapePath(modpath)
	if err != nil {
		die(2, "escape module path %q: %v\n", modpath, err)
	}
	resp, err := http.Get(proxyBase + esc + "/@v/" + version + ".info")
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == http.StatusOK
}

// awaitLanded polls until every (modpath, version) answers on the proxy, or
// dies at landTimeout with resume instructions. The first probe waits
// firstNudgeDelay after the push — probing a fresh tag too early plants a
// negative-cache entry that outlives several honest retries.
func awaitLanded(modpaths []string, version string) {
	time.Sleep(firstNudgeDelay)
	deadline := time.Now().Add(landTimeout)
	pending := append([]string(nil), modpaths...)
	for {
		var still []string
		for _, mp := range pending {
			if proxyLanded(mp, version) {
				stderr("  landed: %s@%s\n", mp, version)
			} else {
				still = append(still, mp)
			}
		}
		if len(still) == 0 {
			return
		}
		if time.Now().After(deadline) {
			die(5, "landing timeout: %s not on the proxy after %s — safe to re-run; every module continues at its first undone step\n",
				strings.Join(still, ", "), landTimeout)
		}
		pending = still
		time.Sleep(pollInterval)
	}
}
