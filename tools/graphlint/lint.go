// Command graphlint validates ontology release documents (YAML) against the
// authoring schema (ontology/schema/graph.schema.json) — the mechanical half of
// doc 02 M3. Pure Go, no CGO.
//
// This v1 enforces the STRUCTURAL contract (required fields, enums, shapes). The
// richer authoring invariants of doc 02 §3.6 that need cross-references — every
// member.variable resolving to a signal, relation targets resolving to phenomena,
// declared spans matching traversal edges, defaults flagged — are tracked as
// follow-on lints (see artifacts/task.md).
package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// result is the outcome of validating one document.
type result struct {
	path string
	err  error // nil => valid
}

// compileSchema loads and compiles the JSON Schema at schemaPath.
func compileSchema(schemaPath string) (*jsonschema.Schema, error) {
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("read schema: %w", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse schema JSON: %w", err)
	}
	c := jsonschema.NewCompiler()
	const id = "graph.schema.json"
	if err := c.AddResource(id, doc); err != nil {
		return nil, fmt.Errorf("add schema resource: %w", err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}
	return sch, nil
}

// validateFile parses a YAML ontology document and validates it against the schema.
func validateFile(sch *jsonschema.Schema, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	var v any
	if err := yaml.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("parse yaml: %w", err)
	}
	return sch.Validate(normalize(v))
}

// normalize converts any map[any]any (defensive; go-yaml v3 already uses string
// keys) into map[string]any so the value is JSON-shaped for the validator.
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = normalize(val)
		}
		return t
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[fmt.Sprintf("%v", k)] = normalize(val)
		}
		return out
	case []any:
		for i, val := range t {
			t[i] = normalize(val)
		}
		return t
	default:
		return v
	}
}

// collect gathers all *.yaml / *.yml files under the given roots (files or dirs).
func collect(roots []string) ([]string, error) {
	var files []string
	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil {
			return nil, fmt.Errorf("stat %q: %w", root, err)
		}
		if !info.IsDir() {
			files = append(files, root)
			continue
		}
		err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if ext := strings.ToLower(filepath.Ext(p)); ext == ".yaml" || ext == ".yml" {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}

// lint compiles the schema and validates every document under roots.
func lint(schemaPath string, roots []string) (results []result, ok bool, err error) {
	sch, err := compileSchema(schemaPath)
	if err != nil {
		return nil, false, err
	}
	files, err := collect(roots)
	if err != nil {
		return nil, false, err
	}
	ok = true
	for _, f := range files {
		e := validateFile(sch, f)
		if e != nil {
			ok = false
		}
		results = append(results, result{path: f, err: e})
	}
	return results, ok, nil
}
