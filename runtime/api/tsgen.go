package api

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// TSFile is the generated TypeScript module, relative to this package.
const TSFile = "ts/samsara-api.gen.ts"

const tsHeader = `// Code generated from runtime/api/schemas by ` + "`go test ./api -run TestTypeScriptUpToDate -update`" + `. DO NOT EDIT.
//
// int64 counters, versions and sequences are decimal strings (Decimal).
// Enum-typed response fields use Open<T>: a newer runtime may send a value
// this build does not know (03 §12.1). Narrow with the is<Enum> guards and
// render anything else as unknown instead of failing.

/** A value of T, or a value added by a newer runtime. */
export type Open<T extends string> = T | (string & {});

/** Returns v when it is one of values, otherwise undefined. */
export function known<T extends string>(values: readonly T[], v: string | null | undefined): T | undefined {
  return typeof v === "string" && (values as readonly string[]).includes(v) ? (v as T) : undefined;
}
`

type tsGen struct {
	defs  map[string]map[string]any // name → schema
	owner map[string]string         // name → file
	out   strings.Builder
}

// GenerateTS renders every schema definition as TypeScript.
func GenerateTS() ([]byte, error) {
	g := &tsGen{defs: map[string]map[string]any{}, owner: map[string]string{}}
	var order []string
	for _, f := range SchemaFiles() {
		doc, err := SchemaDoc(f)
		if err != nil {
			return nil, err
		}
		defs, _ := doc["$defs"].(map[string]any)
		names := make([]string, 0, len(defs))
		for n := range defs {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if prev, dup := g.owner[n]; dup {
				return nil, fmt.Errorf("definition %q is declared in both %s and %s; TypeScript names must be unique", n, prev, f)
			}
			s, ok := defs[n].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s#/$defs/%s is not an object", f, n)
			}
			g.defs[n], g.owner[n] = s, f
			order = append(order, n)
		}
	}
	g.out.WriteString(tsHeader)
	file := ""
	for _, n := range order {
		if g.owner[n] != file {
			file = g.owner[n]
			fmt.Fprintf(&g.out, "\n// ---- %s ----\n", file)
		}
		if err := g.def(n, g.defs[n]); err != nil {
			return nil, fmt.Errorf("%s#/$defs/%s: %w", g.owner[n], n, err)
		}
	}
	return []byte(g.out.String()), nil
}

func isEnum(s map[string]any) bool {
	_, ok := s["enum"].([]any)
	return ok
}

func (g *tsGen) doc(s map[string]any, indent string) {
	if d, ok := s["description"].(string); ok && d != "" {
		fmt.Fprintf(&g.out, "%s/** %s */\n", indent, strings.ReplaceAll(d, "*/", "*\\/"))
	}
}

func (g *tsGen) def(name string, s map[string]any) error {
	g.out.WriteString("\n")
	g.doc(s, "")
	if isEnum(s) {
		lits, err := literals(s["enum"].([]any))
		if err != nil {
			return err
		}
		fmt.Fprintf(&g.out, "export const %sValues = [%s] as const;\n", name, strings.Join(lits, ", "))
		fmt.Fprintf(&g.out, "export type %s = (typeof %sValues)[number];\n", name, name)
		fmt.Fprintf(&g.out, "export function is%s(v: unknown): v is %s {\n  return known(%sValues, typeof v === \"string\" ? v : undefined) !== undefined;\n}\n", name, name, name)
		return nil
	}
	if props, ok := s["properties"].(map[string]any); ok && s["type"] == "object" {
		fmt.Fprintf(&g.out, "export interface %s ", name)
		body, err := g.object(props, s, "")
		if err != nil {
			return err
		}
		g.out.WriteString(body + "\n")
		return nil
	}
	t, err := g.typ(s, "")
	if err != nil {
		return err
	}
	fmt.Fprintf(&g.out, "export type %s = %s;\n", name, t)
	return nil
}

var tsIdent = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

func (g *tsGen) object(props, s map[string]any, indent string) (string, error) {
	req := map[string]bool{}
	if rs, ok := s["required"].([]any); ok {
		for _, r := range rs {
			req[fmt.Sprint(r)] = true
		}
	}
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("{\n")
	inner := indent + "  "
	for _, k := range keys {
		ps, _ := props[k].(map[string]any)
		if d, ok := ps["description"].(string); ok && d != "" {
			fmt.Fprintf(&b, "%s/** %s */\n", inner, strings.ReplaceAll(d, "*/", "*\\/"))
		}
		t, err := g.typ(ps, inner)
		if err != nil {
			return "", fmt.Errorf("%s: %w", k, err)
		}
		key := k
		if !tsIdent.MatchString(k) {
			key = fmt.Sprintf("%q", k)
		}
		opt := "?"
		if req[k] {
			opt = ""
		}
		fmt.Fprintf(&b, "%s%s%s: %s;\n", inner, key, opt, t)
	}
	if ap, ok := s["additionalProperties"].(map[string]any); ok {
		t, err := g.typ(ap, inner)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s[key: string]: %s;\n", inner, t)
	}
	b.WriteString(indent + "}")
	return b.String(), nil
}

func literals(vs []any) ([]string, error) {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		out = append(out, string(b))
	}
	return out, nil
}

func (g *tsGen) ref(r string) (string, error) {
	i := strings.LastIndex(r, "#/$defs/")
	if i < 0 {
		return "", fmt.Errorf("unsupported $ref %q", r)
	}
	n := r[i+len("#/$defs/"):]
	s, ok := g.defs[n]
	if !ok {
		return "", fmt.Errorf("unresolved $ref %q", r)
	}
	if isEnum(s) {
		return "Open<" + n + ">", nil
	}
	return n, nil
}

func union(ts []string) string {
	seen := map[string]bool{}
	var out []string
	for _, t := range ts {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return strings.Join(out, " | ")
}

// typ renders a schema as a TypeScript type expression. It covers the subset
// of JSON Schema used by this package and fails on anything else so a new
// construct is never silently typed as unknown.
func (g *tsGen) typ(s map[string]any, indent string) (string, error) {
	if r, ok := s["$ref"].(string); ok {
		return g.ref(r)
	}
	if c, ok := s["const"]; ok {
		l, err := literals([]any{c})
		return l[0], err
	}
	if e, ok := s["enum"].([]any); ok {
		l, err := literals(e)
		if err != nil {
			return "", err
		}
		for _, v := range e {
			if _, isStr := v.(string); !isStr {
				return union(l), nil
			}
		}
		return "Open<" + union(l) + ">", nil
	}
	var types []string
	switch t := s["type"].(type) {
	case string:
		types = []string{t}
	case []any:
		for _, x := range t {
			types = append(types, fmt.Sprint(x))
		}
	case nil:
		for _, k := range []string{"anyOf", "oneOf"} {
			if alts, ok := s[k].([]any); ok {
				var parts []string
				for _, a := range alts {
					as, _ := a.(map[string]any)
					p, err := g.typ(as, indent)
					if err != nil {
						return "", err
					}
					parts = append(parts, p)
				}
				return union(parts), nil
			}
		}
		for k := range s {
			if k != "description" {
				return "", fmt.Errorf("untyped schema with %q", k)
			}
		}
		return "unknown", nil
	}
	var parts []string
	for _, t := range types {
		switch t {
		case "string":
			parts = append(parts, "string")
		case "integer", "number":
			parts = append(parts, "number")
		case "boolean":
			parts = append(parts, "boolean")
		case "null":
			parts = append(parts, "null")
		case "array":
			items, _ := s["items"].(map[string]any)
			it := "unknown"
			if items != nil {
				var err error
				if it, err = g.typ(items, indent); err != nil {
					return "", err
				}
			}
			if strings.ContainsAny(it, " |") {
				it = "(" + it + ")"
			}
			parts = append(parts, it+"[]")
		case "object":
			if props, ok := s["properties"].(map[string]any); ok {
				o, err := g.object(props, s, indent)
				if err != nil {
					return "", err
				}
				parts = append(parts, o)
			} else if ap, ok := s["additionalProperties"].(map[string]any); ok {
				v, err := g.typ(ap, indent)
				if err != nil {
					return "", err
				}
				parts = append(parts, "Record<string, "+v+">")
			} else {
				parts = append(parts, "Record<string, unknown>")
			}
		default:
			return "", fmt.Errorf("unsupported type %q", t)
		}
	}
	return union(parts), nil
}
