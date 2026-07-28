# clone-srctree

Mirror a Go source tree from `-from` to `-to` (clone = `cp -r` + delete-stale), with
target-side protections, importmap-driven `.go` and `go.mod` rewrites, a rewrite-residue
check, import-order fixing (via the `sortimports` package), and a build check. Replaces
error-prone manual sync steps.

## Install

```bash
go install github.com/x64c/srcutils/go/clone-srctree@latest
```

## Usage

```bash
clone-srctree -from ./base -to ./p                # plain clone
clone-srctree -conf p.json                        # clone + importmap rewrites/generation
clone-srctree -conf p.json -dry                   # show the plan, change nothing
```

Run order (real run):

1. **delete** target files with no counterpart in source (`-keep` protected), then prune emptied dirs
2. **copy** every source file into target, overwriting, except source `-skip`s and target `-keep`s
   (raw `go.mod`/`go.sum` are excluded when generation applies)
3. **rewrite** derived importmap replacements in target `.go` files
4. **verify** no `"K/` or `"K"` residue remains in **any** target `.go` file (incl. keep-protected)
5. **generate** each non-kept `go.mod` from source (parsed via `modfile`), resolve pins; `go mod tidy` per module only on `buildtest=module` runs
6. **sort-imports** on the target (always runs)
7. **build** check per `-build`

## Flags

| Flag | Meaning |
| --- | --- |
| `-from <dir>` | Source tree (required). |
| `-to <dir>` | Target tree (required). Must not overlap `-from`. |
| `-keep <pattern>` | Target-side protection (repeatable). Never overwritten, deleted, or walked into. **No defaults.** |
| `-skip <pattern>` | Source-side exclusion (repeatable). No defaults. |
| `-buildtest <mode>` | `module`, `workspace`, or `both`; absent = no build test. |
| `-conf <file>` | JSON config (keys below); explicit flags override conf values. |
| `-dry` | Print the plan; change nothing. |
| `-v` | Print version (from embedded build info) and exit. |
| `-vv` | Verbose per-file logging (in `-dry`, dumps each generated `go.mod`). |

**No path is special — `.git` included.** The tool has no built-in protections and no
built-in skips: a VCS-controlled target protects its metadata dir explicitly
(`"keep": ["/.git"]`), a VCS-controlled source excludes its own (`"skip": [".git"]`),
and the first `-dry`'s deletion list is the guard — read it. The only path *couplings*
are structural, not protective: a `go.sum` always follows its sibling `go.mod`'s fate,
and `go.work.sum` follows its `go.work` — never synced or deleted independently.

To seed a target repo from a source's history (e.g. staging a large migration as one
reviewable diff), that is a git operation, done with git — `git clone srctree1 srctree2`
(works with plain local paths, hardlinked and fast) — then run `clone-srctree` with
`/.git` in `keep`: the mirror transforms the content around the repo, and `git status`
shows the whole move as an uncommitted diff, ready to be one commit.

## Config (`-conf`) — schema v3

```json
{
  "from": "…", "to": "…",
  "keep": ["/.git", "/README.md"], "skip": [],
  "version": "v0.2.0",
  "goenv": {"GOEXPERIMENT": "jsonv2"},
  "importmap": {
    "foo.base": "github.com/bar/foo/base",
    "old/mod":  {"to": "new/mod", "replace": "../local"}
  },
  "buildtest": "module"
}
```

`goenv` (conf-only, optional) declares the **cloned family's toolchain requirements** —
env vars applied to every `go` subprocess of the run (`go mod tidy`, buildtest builds,
the stdlib listing behind import sorting). They belong in the conf because they are a
property of the code being cloned, not of the machine or the shell: a family whose code
needs a `GOEXPERIMENT` needs it in every invocation that compiles that code — including
its consumers' builds, until the experiment graduates. Conf values override the
inherited environment (the conf is the authority on what the family needs).

`importmap` is **conf-only** (a flags-only run performs no rewrites or generation). A
value is the mapped module path — a bare string, or an object `{"to": …, "replace": …}`
when a replace rides along.

**Versions live in ONE root-level `"version"`**, not per entry. It pins every *consumed*
entry — one some generated go.mod `require`s (a conf with consumed entries but no
`version` = hard error naming the module; a `version` nothing consumes = hard error, dead
field). An entry for a module *discovered under `from`* is an identity mapping — module
line + import rewrites — and never takes a version: a module never states its own version,
now structurally (there is no field to put one in). One conf = one consumed version; a
tree needing two different pins is two relationships = two confs. `@latest` is allowed
but must be written out (resolved via `go list -m <to>@latest` on real runs). Unknown
entry keys are rejected; retired forms (`rw`, per-entry `version`, boolean `buildtest`)
get loud migration errors.

## Prefixed mode (`"importprefix"`)

```json
{
  "from": "/path/to/foo", "to": "/path/to/bar",
  "version": "v0.2.0",
  "importprefix": "github.com/bar/foo/",
  "exclude": ["baz"],
  "keep": ["/.git", "/.gitignore", "/base/README.md"],
  "importmap": { "foo.base": "base", "foo.kvdbs/redis": "kvdbs/redis" },
  "buildtest": "both"
}
```

`importprefix` (conf-only, must end `/`) switches the conf to family semantics: one conf
mirrors a whole multi-module tree into a monorepo root.

- Importmap values become **prefix-relative** (mapped path = `importprefix` + value) and
  **double as each module's target directory** under `to` — the `base → gw` rename is the
  mapping itself, no dir table.
- Every discovered module must be mapped or listed in `exclude` (`from`-relative dirs,
  must exist) — a module mapping outside the prefix cannot derive a target dir and errors.
- The clone domain is the union of module subtrees: source files outside them are not
  copied, and target paths that map back to no source are stale. **Target-root extras
  (READMEs, `.gitignore`) must be in `keep` or they are deleted — read the first `-dry`
  carefully.** (`go.work` is not an extra — see below.)

Without `importprefix` (pair mode) values are full module paths, roots pair directly and
relative dirs are preserved — the classic one-conf-per-subtree fleet.

## go.work — clone content, not test machinery

Cloning a workspace clones its workspace-ness, the same way module-ness is cloned by
generating go.mods. Driven by the **source's shape**, never by `buildtest`:

- **Source has exactly one `go.work`** → the target counterpart lives at the
  *corresponding location* (same relative path; mapped if inside a module subtree).
  Kept there → hand-maintained, no generation. Not kept → **generated**: `use` dirs
  mapped through the geometry, entries for excluded modules dropped, replaces targeting
  mapped modules dropped, `go` line carried. A *kept* go.work anywhere else = mismatch,
  hard error. `go.work.sum` follows its go.work — never copied, never deleted
  independently (workspace-mode builds maintain it).

  Family confs (`importprefix` + `version`) additionally get a **version-bridge
  replace block** — `replace ( <module> <version> => ./<dir> )`, one line per
  **consumed** workspace module (one whose path some generated go.mod pins).
  A `use` directive supplies a module's *content* but does not satisfy the
  version *requirement* another workspace module's go.mod declares: on a family
  version bump those pins name a not-yet-published tag, and without the bridge
  the module graph fails to load for the whole workspace. With it,
  `buildtest: "workspace"` can verify the family **before** its tags exist; once
  they are published the bridge is inert. Unconsumed modules get no bridge —
  there is no requirement edge to redirect, and dead replace directives draw
  "unused" warnings in IDEs. Carried-over foreign replaces are emitted as their
  own parenthesized block after it.
- **Source has none** → no generation; a kept target-only go.work survives, an unkept
  one is stale.
- **Source has many** → the tree is mid-merge: cloning only, go.works mirror verbatim
  (`use` paths NOT remapped), and **any** `buildtest` is a hard error — no verdict on a
  multi-workspace tree is trustworthy.

### What importmap does

For each key `K` → `to` `T`:

- **`.go` rewrites:** `"K/` → `"T/` and exact `"K"` → `"T"` (longest key first).
- **`go.mod` generation** (per non-kept `go.mod`): module line mapped if its path is a key;
  direct requires whose path is a key rewritten to `T`+`version` (others copied verbatim);
  `// indirect` requires dropped (tidy recomputes); replaces targeting a mapped module
  dropped; one `replace T => <replace>` emitted per entry that has `replace`; then, on
  `buildtest=module` runs only, `go mod tidy` regenerates `go.sum`. Tidy is module-alone
  by Go's own design (workspace-blind), so it is honest only once the pinned versions are
  **published** — a `workspace` run is the pre-publish world (family pins may name tags
  that don't exist yet) and must not tidy; the go.mods land as generated and the next
  module-alone run completes `go.sum`. If no importmap is present, `go.mod`/`go.sum` are
  ordinary files, copied verbatim.

## Pattern semantics

- Bare name (`go.mod`) matches that basename at **any depth**.
- Leading `/` (`/README.md`) anchors to the **root**.
- `*` globs within a single path segment (no `**`).
- A pattern matching a directory protects its entire subtree.

## Build-test (`-buildtest <mode>`, conf `"buildtest": "<mode>"`)

Opt-in verification — a TEST, not a product: nothing it produces survives or lands in
the tree, it always runs **after** the whole clone (never mid-pipeline), and its mode
never changes what the clone wrote. Absent = the tool clones, full stop. The conf
declares **one question**; the source's workspace shape only validates that the question
is askable:

- **`module`** — self-build with `GOWORK=off` pinned, over the **generated set** (the
  modules this run's importmap produced — a module-alone build resolves deps from the
  proxy, so testing anything else tests the published world, not the clone): do the
  go.mod claims resolve against the **published** world? Identical on any machine (an
  ambient go.work is ignored and reported).
- **`workspace`** — every build runs with `GOWORK=` pinned to the target go.work at the
  corresponding location (generated or kept there): does the family cohere under **its
  own** workspace? Never an ancestor's, never a nested stray's. Errors when the source
  has no go.work; any buildtest errors when it has several (mid-merge tree).

The two verdicts remain a diagnostic pair across *runs*: module ❌ + workspace ✅ =
healthy transitional state (publish the dependency, watch module mode flip green).
Conf declares the steady-state question; a flag asks the other per run
(`-conf x.json -buildtest workspace`).

- Flags-only runs (no importmap) auto-detect layout: root `go.mod` → one compile; else
  every dir containing a `go.mod`.
- Main-package binaries go to a throwaway temp dir (deleted after); library packages
  compile to the build cache. Either way the tree stays byte-clean.
- A failed build-test exits 3 — the clone itself already happened; the TEST failed.
- `go mod tidy` (generation, `buildtest=module` runs only) is always `GOWORK=off` —
  generated go.mods resolve against the published world regardless of any surrounding
  workspace.

The environment is inherited; set `GOEXPERIMENT` yourself if needed.

## Exit codes

`0` success · `1` usage/config · `2` verify residue · `3` build / `go mod tidy` failure · `4` I/O failure.
