package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/x64c/srcutils/go/internal/toolver"
)

// clone-srctrees runs clone-srctree once per conf file, in argument order.
// Composition over shared code: the singular tool owns the engine; this loop
// only orchestrates and summarizes.

func main() {
	failFast := false
	execPath := ""
	var passthrough []string // -dry / -vv forwarded to every run
	var confs []string
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-fail-fast":
			failFast = true
		case "-v":
			toolver.Print()
			os.Exit(0)
		case "-dry", "-vv":
			passthrough = append(passthrough, args[i])
		case "-exec":
			i++
			if i >= len(args) {
				stderr("-exec: missing <path>\n")
				os.Exit(1)
			}
			execPath = args[i]
		case "-h", "-help", "--help":
			usage()
			os.Exit(1)
		default:
			expanded, err := expandConfArg(args[i])
			if err != nil {
				stderr("%v\n", err)
				os.Exit(1)
			}
			confs = append(confs, expanded...)
		}
	}
	if len(confs) == 0 {
		usage()
		os.Exit(1)
	}

	bin, err := findCloneSrctree(execPath)
	if err != nil {
		stderr("%v\n", err)
		os.Exit(1)
	}

	type outcome struct {
		conf string
		err  error
	}
	var outcomes []outcome
	for _, conf := range confs {
		fmt.Printf("==== clone-srctree -conf %s ====\n", conf)
		cmd := exec.Command(bin, append([]string{"-conf", conf}, passthrough...)...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		runErr := cmd.Run()
		outcomes = append(outcomes, outcome{conf, runErr})
		if runErr != nil && failFast {
			break
		}
	}

	fmt.Println("\n======== clone-srctrees summary ========")
	failed := 0
	for _, o := range outcomes {
		if o.err != nil {
			failed++
			fmt.Printf("FAIL  %s  (%v)\n", o.conf, o.err)
		} else {
			fmt.Printf("ok    %s\n", o.conf)
		}
	}
	if skipped := len(confs) - len(outcomes); skipped > 0 {
		fmt.Printf("(%d conf(s) skipped after -fail-fast)\n", skipped)
	}
	if failed > 0 {
		os.Exit(2)
	}
}

// expandConfArg resolves one argument: a conf file as-is, or a directory
// expanded to its *.json files sorted by name.
func expandConfArg(arg string) ([]string, error) {
	info, err := os.Stat(arg)
	if err != nil {
		return nil, fmt.Errorf("conf %s: %v", arg, err)
	}
	if !info.IsDir() {
		return []string{arg}, nil
	}
	matches, err := filepath.Glob(filepath.Join(arg, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("conf dir %s: %v", arg, err)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("conf dir %s: no *.json files", arg)
	}
	sort.Strings(matches)
	return matches, nil
}

// findCloneSrctree resolves the engine binary: -exec verbatim, else PATH.
func findCloneSrctree(execPath string) (string, error) {
	if execPath != "" {
		info, err := os.Stat(execPath)
		if err != nil {
			return "", fmt.Errorf("-exec %s: %v", execPath, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("-exec %s: is a directory", execPath)
		}
		return execPath, nil
	}
	if p, err := exec.LookPath("clone-srctree"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("clone-srctree not on PATH; go install github.com/x64c/srcutils/go/clone-srctree@latest (or use -exec <path>)")
}

func usage() {
	stderr("usage: clone-srctrees [-fail-fast] [-dry] [-vv] [-exec <path>] <conf.json|conf-dir> [...]\n\n")
	stderr("Runs clone-srctree -conf <file> for each conf, in order, and summarizes.\n")
	stderr("A directory argument expands to its *.json files, sorted by name.\n")
	stderr("-exec <path> runs that clone-srctree binary; default finds it on PATH.\n")
	stderr("-dry and -vv are forwarded to every run; -v prints the version.\n")
	stderr("Default: continue past a failed conf and report at the end;\n")
	stderr("-fail-fast stops at the first failure.\n")
	stderr("Exit: 0 all ok, 1 usage, 2 at least one run failed.\n")
}

func stderr(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format, args...)
}
