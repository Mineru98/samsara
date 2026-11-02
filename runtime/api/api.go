// Package api holds the wire contract of the runtime (03 §12.2): the OpenAPI
// document, the JSON Schemas for HTTP, SSE, IPC, plan and config payloads,
// the generated TypeScript types and the contract fixtures. The schemas are
// the single source; Go structs are checked against them in tests.
package api

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// BaseURL is the $id prefix of every schema file.
const BaseURL = "https://samsara.local/schemas/"

//go:embed schemas/*.schema.json
var schemaFS embed.FS

// SchemaFiles returns the schema file names in a stable order.
func SchemaFiles() []string {
	ents, _ := fs.ReadDir(schemaFS, "schemas")
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// SchemaDoc returns one decoded schema file.
func SchemaDoc(name string) (map[string]any, error) {
	b, err := schemaFS.ReadFile(path.Join("schemas", name))
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return m, nil
}

type validator struct {
	once  sync.Once
	err   error
	c     *jsonschema.Compiler
	mu    sync.Mutex
	cache map[string]*jsonschema.Schema
}

var (
	// tolerant follows §12.1: additive optional fields are accepted.
	tolerant = &validator{}
	// strict also rejects properties the schema does not declare, so a Go
	// field added without a schema change fails the contract tests.
	strict = &validator{}
)

func (v *validator) init(closed bool) error {
	v.once.Do(func() {
		c := jsonschema.NewCompiler()
		c.DefaultDraft(jsonschema.Draft2020)
		c.AssertFormat()
		for _, name := range SchemaFiles() {
			doc, err := SchemaDoc(name)
			if err != nil {
				v.err = err
				return
			}
			if closed {
				closeObjects(doc)
			}
			if err := c.AddResource(BaseURL+name, doc); err != nil {
				v.err = err
				return
			}
		}
		v.c, v.cache = c, map[string]*jsonschema.Schema{}
	})
	return v.err
}

// closeObjects adds additionalProperties:false to every object schema that
// declares properties and leaves additionalProperties unspecified.
func closeObjects(n any) {
	switch t := n.(type) {
	case map[string]any:
		if _, ok := t["properties"].(map[string]any); ok {
			if _, set := t["additionalProperties"]; !set {
				t["additionalProperties"] = false
			}
		}
		for _, c := range t {
			closeObjects(c)
		}
	case []any:
		for _, c := range t {
			closeObjects(c)
		}
	}
}

func (v *validator) schema(ref string, closed bool) (*jsonschema.Schema, error) {
	if err := v.init(closed); err != nil {
		return nil, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if s, ok := v.cache[ref]; ok {
		return s, nil
	}
	s, err := v.c.Compile(BaseURL + ref)
	if err != nil {
		return nil, err
	}
	v.cache[ref] = s
	return s, nil
}

func validate(v *validator, closed bool, ref string, data []byte) error {
	s, err := v.schema(ref, closed)
	if err != nil {
		return err
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("%s: invalid JSON: %w", ref, err)
	}
	if err := s.Validate(inst); err != nil {
		return fmt.Errorf("%s: %w", ref, err)
	}
	return nil
}

// Ref names a schema definition: "<file>#/$defs/<Name>", or a bare file name
// for documents with a root schema (plan, config).
func Ref(file, def string) string {
	if def == "" {
		return file
	}
	return file + "#/$defs/" + def
}

// Validate checks JSON against a schema definition as a client would:
// undeclared properties are allowed (03 §12.1).
func Validate(ref string, data []byte) error { return validate(tolerant, false, ref, data) }

// ValidateStrict checks JSON produced by this runtime: undeclared properties
// are errors, so the schema and the Go structs cannot drift apart.
func ValidateStrict(ref string, data []byte) error { return validate(strict, true, ref, data) }
