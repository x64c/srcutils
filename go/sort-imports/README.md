# sort-imports

Merge all Go import declarations into a single block with two groups (stdlib, others) separated by a blank line, each group alphabetically sorted. Single import uses no parentheses.

## Install

```bash
go install github.com/x64c/srcutils/go/sort-imports@latest
```

## Usage

```bash
# Single file
sort-imports path/to/file.go

# Entire source tree (concurrent)
sort-imports path/to/src/
```

Prints each modified file path to stdout. Files already sorted are skipped.

## Stdlib detection

Uses `go list std` at runtime, inheriting your environment. For experimental packages (e.g. `encoding/json/v2`), set `GOEXPERIMENT`:

```bash
GOEXPERIMENT=jsonv2 sort-imports ./src
```

## Precautions

- Standalone comment lines between import declarations are removed.
- Inline comments (e.g. `"foo" // bar`) are preserved.
