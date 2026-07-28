package sortimports

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// collectGoFiles walks a directory tree and returns all .go file paths.
func collectGoFiles(root string) ([]string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".go") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// ProcSrcTree walks a directory tree and runs ProcSrcFile on every .go file
// concurrently. Prints each modified file path to stdout.
func ProcSrcTree(root string) error {
	files, err := collectGoFiles(root)
	if err != nil {
		return err
	}

	workers := runtime.NumCPU()
	sem := make(chan struct{}, workers)

	var mu sync.Mutex
	var firstErr error

	var wg sync.WaitGroup
	for _, path := range files {
		wg.Add(1)
		sem <- struct{}{}
		go func(p string) {
			defer wg.Done()
			defer func() { <-sem }()

			modified, err := ProcSrcFile(p)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("%s: %w", p, err)
				}
				mu.Unlock()
				return
			}
			if modified {
				mu.Lock()
				fmt.Println(p)
				mu.Unlock()
			}
		}(path)
	}

	wg.Wait()
	return firstErr
}
