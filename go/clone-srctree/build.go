package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// buildTestDirs returns the module directories the build-test covers. With an
// importmap, that is the GENERATED set — the discovered source modules' target
// dirs, minus kept ones (a module-alone build resolves deps from the proxy, so
// testing a module this run didn't generate tests the published world, not the
// clone). Flags-only runs fall back to layout auto-detection.
func buildTestDirs(cfg Config, tm treeMap, srcMods []string, keeps patternSet) []string {
	if cfg.ImportMap == nil {
		if fileExists(filepath.Join(cfg.To, "go.mod")) {
			return []string{cfg.To}
		}
		return findModuleDirs(cfg.To)
	}
	var dirs []string
	for _, relDir := range srcMods {
		trel, ok := tm.srcToTarget(relDir)
		if !ok || isKeptPath(keeps, path.Join(trel, "go.mod")) {
			continue
		}
		dirs = append(dirs, filepath.Join(cfg.To, filepath.FromSlash(trel)))
	}
	sort.Strings(dirs)
	return dirs
}

// findModuleDirs returns every directory under root that contains a go.mod.
func findModuleDirs(root string) []string {
	var dirs []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && fileExists(filepath.Join(p, "go.mod")) {
			dirs = append(dirs, p)
		}
		return nil
	})
	sort.Strings(dirs)
	return dirs
}

// ambientGoWork reports the go.work file governing dir ("" if none). Only used
// to REPORT that module mode is ignoring one — never to select a workspace.
func ambientGoWork(dir string) string {
	cmd := exec.Command("go", "env", "GOWORK")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	w := strings.TrimSpace(string(out))
	if w == "off" {
		return ""
	}
	return w
}

// hasMainPackage reports whether any package under dir is package main.
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

// buildTestStep test-compiles the cloned tree when requested (-buildtest).
// It is a TEST, not a product, and always runs AFTER the whole clone: nothing
// it produces survives, and its mode never changes what the clone wrote.
//
// The conf declares which QUESTION is asked; the source's workspace shape
// only validates that the question is askable (see goworkPlan):
//
//	"module"    — self-build with GOWORK=off pinned: do the generated go.mod
//	              claims resolve against the PUBLISHED world?
//	"workspace" — build with GOWORK pinned to the target go.work at the
//	              corresponding location (generated or kept there): does the
//	              family cohere under ITS OWN workspace? Never an ancestor's,
//	              never a nested stray's. Unaskable when the source has no
//	              go.work; a multi-workspace source refuses any buildtest.
func buildTestStep(cfg Config, tm treeMap, srcMods []string, keeps patternSet, plan goworkPlan, dry bool) string {
	if cfg.BuildTest == "" {
		return "skipped (not requested)"
	}
	if plan.count() > 1 {
		die(1, "buildtest %q: source has %d go.work files — cloning only (no test verdict is trustworthy on a multi-workspace tree; finish the merge first)\n",
			cfg.BuildTest, plan.count())
	}
	dirs := buildTestDirs(cfg, tm, srcMods, keeps)
	if len(dirs) == 0 {
		return "no go.mod found; skipped"
	}

	var env []string
	var suffix string
	switch cfg.BuildTest {
	case "module":
		env = append(os.Environ(), "GOWORK=off")
		suffix = " [module-alone]"
		if ambientGoWork(cfg.To) != "" {
			suffix = " [module-alone; ambient go.work ignored]"
		}
	case "workspace":
		if plan.count() == 0 {
			die(1, "buildtest workspace: source %s is not a workspace (no go.work)\n", cfg.From)
		}
		gowork := filepath.Join(cfg.To, filepath.FromSlash(plan.tgtRel))
		env = append(os.Environ(), "GOWORK="+gowork)
		suffix = fmt.Sprintf(" [workspace: %s]", gowork)
	default:
		die(1, "invalid -buildtest %q (want module or workspace)\n", cfg.BuildTest)
	}

	if dry {
		return fmt.Sprintf("%d module(s) (planned)%s", len(dirs), suffix)
	}
	binDir, err := os.MkdirTemp("", "clone-srctree-buildtest-")
	if err != nil {
		die(4, "buildtest tempdir: %v\n", err)
	}
	defer os.RemoveAll(binDir)
	for _, d := range dirs {
		args := []string{"build", "./..."}
		if hasMainPackage(d, env) {
			// -o only when mains exist: go refuses -o for main-less patterns,
			// and library-only builds cannot leave residue anyway.
			args = []string{"build", "-o", binDir, "./..."}
		}
		cmd := exec.Command("go", args...)
		cmd.Dir = d
		cmd.Env = env
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			die(3, "buildtest (%s) failed in %s: %v\n", cfg.BuildTest, d, err)
		}
	}
	return fmt.Sprintf("ok, %d module(s)%s", len(dirs), suffix)
}
