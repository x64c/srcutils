package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Config is the resolved run configuration after merging conf and flags.
type Config struct {
	From         string
	To           string
	Keep         []string
	Skip         []string
	Exclude      []string               // conf-only: source subtrees not cloned (must exist)
	Version      string                 // conf-only: the one version consumed entries are pinned at
	ImportPrefix string                 // conf-only: "" = pair mode; non-empty = prefixed mode (values relative)
	GoEnv        map[string]string      // conf-only: env applied to this process (and so every go subprocess) — the cloned family's toolchain requirements
	BuildTest    string                 // "" = skip; "module" | "workspace" | "both"
	ImportMap    map[string]importEntry // nil = absent (flags-only run: no rewrites/generation)
	Dry          bool
	Verbose      bool
}

// importEntry is one resolved importmap value. To is always the full target
// module path; Dir is the prefix-relative value (prefixed mode only), which
// doubles as the module's target directory.
type importEntry struct {
	To      string
	Dir     string
	Replace string
}

// confFile mirrors the JSON config. Pointer fields distinguish "absent" from "zero".
type confFile struct {
	From         *string               `json:"from"`
	To           *string               `json:"to"`
	Keep         *[]string             `json:"keep"`
	Skip         *[]string             `json:"skip"`
	Exclude      *[]string             `json:"exclude"`
	Version      *string               `json:"version"`
	ImportPrefix *string               `json:"importprefix"`
	GoEnv        *map[string]string    `json:"goenv"`
	BuildTest    *buildtestMode        `json:"buildtest"`
	ImportMap    map[string]*confEntry `json:"importmap"`
}

// buildtestMode decodes the buildtest conf value with a clear migration error
// for the retired boolean form.
type buildtestMode string

func (m *buildtestMode) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf(`buildtest is now "module" or "workspace" (or omit the key); booleans are retired`)
	}
	if s == "both" {
		return fmt.Errorf(`buildtest: "both" was removed — the conf declares ONE question; ask the other per run via -buildtest`)
	}
	if s != "module" && s != "workspace" {
		return fmt.Errorf(`buildtest: invalid mode %q (want "module" or "workspace", or omit the key)`, s)
	}
	*m = buildtestMode(s)
	return nil
}

// confEntry is one importmap value in the conf: a bare string (the mapped
// path), or an object {"to": ..., "replace": ...} when a replace rides along.
// Unknown keys are rejected; the retired per-entry 'version' gets a migration
// error pointing at the root-level "version".
type confEntry struct {
	To      string
	Replace string
}

func (e *confEntry) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		e.To = s
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for k := range raw {
		switch k {
		case "to", "replace":
		case "version":
			return fmt.Errorf(`schema v3: per-entry 'version' was removed; declare the root-level "version" instead (one conf = one consumed version)`)
		default:
			return fmt.Errorf("unknown key %q", k)
		}
	}
	if v, ok := raw["to"]; ok {
		if err := json.Unmarshal(v, &e.To); err != nil {
			return fmt.Errorf("'to': %v", err)
		}
	}
	if v, ok := raw["replace"]; ok {
		if err := json.Unmarshal(v, &e.Replace); err != nil {
			return fmt.Errorf("'replace': %v", err)
		}
	}
	return nil
}

// loadConf reads a JSON v3 config file and applies its present keys onto cfg.
func loadConf(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	// Retired v1 key: fail loudly instead of silently ignoring it.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if _, ok := probe["rw"]; ok {
		return fmt.Errorf("%s: schema v2: 'rw' was removed; use 'importmap'", path)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields() // stray-key protection, top level and per entry
	var c confFile
	if err := dec.Decode(&c); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}

	if c.From != nil {
		cfg.From = *c.From
	}
	if c.To != nil {
		cfg.To = *c.To
	}
	if c.Keep != nil {
		cfg.Keep = *c.Keep
	}
	if c.Skip != nil {
		cfg.Skip = *c.Skip
	}
	if c.Exclude != nil {
		cfg.Exclude = *c.Exclude
	}
	if c.Version != nil {
		cfg.Version = *c.Version
	}
	if c.ImportPrefix != nil {
		cfg.ImportPrefix = *c.ImportPrefix
	}
	if c.GoEnv != nil {
		for k := range *c.GoEnv {
			if k == "" {
				return fmt.Errorf("%s: goenv: empty variable name", path)
			}
		}
		cfg.GoEnv = *c.GoEnv
	}
	if c.BuildTest != nil {
		cfg.BuildTest = string(*c.BuildTest)
	}

	if c.ImportPrefix != nil {
		p := *c.ImportPrefix
		if p == "" || p == "/" || !strings.HasSuffix(p, "/") {
			return fmt.Errorf(`%s: importprefix %q must be a non-empty module path prefix ending in "/"`, path, p)
		}
		if c.ImportMap == nil {
			return fmt.Errorf("%s: importprefix requires an importmap", path)
		}
	}
	if c.Exclude != nil && c.ImportMap == nil {
		return fmt.Errorf("%s: exclude requires an importmap", path)
	}
	if c.Version != nil && c.ImportMap == nil {
		return fmt.Errorf("%s: version requires an importmap (it pins consumed mapped modules)", path)
	}

	if c.ImportMap != nil {
		cfg.ImportMap = map[string]importEntry{}
		for k, e := range c.ImportMap {
			if e == nil || e.To == "" {
				return fmt.Errorf("%s: importmap[%q]: mapped path is required and non-empty", path, k)
			}
			if cfg.ImportPrefix != "" {
				// prefixed mode: values are importprefix-relative and double as
				// the module's target directory.
				v := e.To
				if strings.HasPrefix(v, "/") || strings.HasSuffix(v, "/") {
					return fmt.Errorf(`%s: importmap[%q]: value %q must not start or end with "/" (it is importprefix-relative)`, path, k, v)
				}
				if strings.HasPrefix(v, cfg.ImportPrefix) {
					return fmt.Errorf("%s: importmap[%q]: value %q already carries the importprefix", path, k, v)
				}
				cfg.ImportMap[k] = importEntry{To: cfg.ImportPrefix + v, Dir: v, Replace: e.Replace}
			} else {
				cfg.ImportMap[k] = importEntry{To: e.To, Replace: e.Replace}
			}
		}
	}
	return nil
}

// keepPatterns returns the target-side protection patterns (only what -keep supplies).
func (c *Config) keepPatterns() []string {
	return c.Keep
}

// skipPatterns returns the source-side exclusion patterns (excluded subtrees
// are root-anchored skips). Nothing is skipped by default — .git included; a
// git source that shouldn't mirror its metadata says so in the conf.
func (c *Config) skipPatterns() []string {
	out := append([]string{}, c.Skip...)
	for _, e := range c.Exclude {
		out = append(out, "/"+strings.Trim(e, "/"))
	}
	return out
}

// stringList is a repeatable string flag.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}
