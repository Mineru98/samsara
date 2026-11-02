// Package apitest lets runtime tests check real responses against the wire
// contract and refresh the shared contract fixtures from them.
//
// Fixtures are rewritten only when SAMSARA_UPDATE_FIXTURES=1; a normal test run
// validates the live payloads and leaves the files alone.
package apitest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/Mineru98/samsara/runtime/api"
)

// Payload is one wire document of a fixture, tagged with its schema.
type Payload struct {
	Schema string `json:"schema"`
	Note   string `json:"note,omitempty"`
	// Expect is empty for payloads the runtime emits (strictly valid),
	// "tolerant" for additive fields only clients accept, and "reject" for
	// values outside this contract (e.g. a future enum value).
	Expect string          `json:"expect,omitempty"`
	Data   json.RawMessage `json:"data"`
}

// Fixture is the shared Go/TypeScript contract fixture file format.
type Fixture struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Payloads    []Payload `json:"payloads"`
}

// FixtureDir is the absolute fixtures directory of the api package.
func FixtureDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "fixtures")
}

// Check validates v strictly against ref and returns its JSON.
func Check(t testing.TB, ref string, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := api.ValidateStrict(ref, b); err != nil {
		t.Fatalf("contract violation: %v\npayload: %s", err, b)
	}
	return b
}

// Recorder collects validated payloads for one fixture.
type Recorder struct {
	t       testing.TB
	fx      Fixture
	replace map[string]string
}

// NewRecorder starts a fixture. replace maps machine-specific substrings
// (temporary directories) to stable placeholders.
func NewRecorder(t testing.TB, name, description string, replace map[string]string) *Recorder {
	return &Recorder{t: t, fx: Fixture{Name: name, Description: description}, replace: replace}
}

// Add validates v strictly and appends it to the fixture.
func (r *Recorder) Add(ref, note string, v any) {
	r.t.Helper()
	r.fx.Payloads = append(r.fx.Payloads, Payload{Schema: ref, Note: note, Data: Check(r.t, ref, v)})
}

// AddRaw validates already-encoded JSON strictly and appends it.
func (r *Recorder) AddRaw(ref, note string, b []byte) {
	r.t.Helper()
	if err := api.ValidateStrict(ref, b); err != nil {
		r.t.Fatalf("contract violation: %v\npayload: %s", err, b)
	}
	r.fx.Payloads = append(r.fx.Payloads, Payload{Schema: ref, Note: note, Data: append(json.RawMessage(nil), b...)})
}

// Save writes the fixture when SAMSARA_UPDATE_FIXTURES=1.
func (r *Recorder) Save() {
	r.t.Helper()
	if os.Getenv("SAMSARA_UPDATE_FIXTURES") != "1" {
		return
	}
	n := newNormalizer(r.replace)
	for i := range r.fx.Payloads {
		var v any
		if err := json.Unmarshal(r.fx.Payloads[i].Data, &v); err != nil {
			r.t.Fatal(err)
		}
		b, err := json.Marshal(n.walk(v, ""))
		if err != nil {
			r.t.Fatal(err)
		}
		if err := api.ValidateStrict(r.fx.Payloads[i].Schema, b); err != nil {
			r.t.Fatalf("normalized fixture %s breaks the contract: %v", r.fx.Name, err)
		}
		r.fx.Payloads[i].Data = b
	}
	out, err := json.MarshalIndent(r.fx, "", "  ")
	if err != nil {
		r.t.Fatal(err)
	}
	path := filepath.Join(FixtureDir(), r.fx.Name+".json")
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		r.t.Fatal(err)
	}
	r.t.Logf("wrote %s", path)
}

var (
	idRe     = regexp.MustCompile(`\b([a-z]+)_[0-9a-f]{24}\b`)
	sha40Re  = regexp.MustCompile(`\b[0-9a-f]{40}\b`)
	sha64Re  = regexp.MustCompile(`\b[0-9a-f]{64}\b`)
	opaqueKs = map[string]bool{"snapshot_cursor": true, "next_cursor": true}
)

// normalizer replaces random ids, hashes and temp paths with placeholders
// numbered in payload order. Rows created at the same instant are ordered by
// their random id, so a regenerated fixture may permute placeholders even when
// the contract did not change; review fixture diffs with that in mind.
type normalizer struct {
	replace []string
	ids     map[string]string
	count   map[string]int
}

func newNormalizer(replace map[string]string) *normalizer {
	keys := make([]string, 0, len(replace))
	for k := range replace {
		keys = append(keys, k)
	}
	// Longest first so nested directories win over their parents.
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	var pairs []string
	for _, k := range keys {
		pairs = append(pairs, k, replace[k])
	}
	return &normalizer{replace: pairs, ids: map[string]string{}, count: map[string]int{}}
}

func (n *normalizer) stable(kind, old string, width int) string {
	if v, ok := n.ids[old]; ok {
		return v
	}
	n.count[kind]++
	var v string
	if width == 0 {
		v = fmt.Sprintf("%s_fx%03d", kind, n.count[kind])
	} else {
		v = fmt.Sprintf("%0*x", width, n.count[kind])
	}
	n.ids[old] = v
	return v
}

func (n *normalizer) str(s string) string {
	if len(n.replace) > 0 {
		s = strings.NewReplacer(n.replace...).Replace(s)
	}
	s = idRe.ReplaceAllStringFunc(s, func(m string) string {
		return n.stable(idRe.FindStringSubmatch(m)[1], m, 0)
	})
	s = sha64Re.ReplaceAllStringFunc(s, func(m string) string { return n.stable("sha256", m, 64) })
	return sha40Re.ReplaceAllStringFunc(s, func(m string) string { return n.stable("sha1", m, 40) })
}

func (n *normalizer) walk(v any, key string) any {
	switch t := v.(type) {
	case map[string]any:
		// Visit keys in order so placeholder numbering is reproducible.
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(t))
		for _, k := range keys {
			out[k] = n.walk(t[k], k)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, c := range t {
			out[i] = n.walk(c, key)
		}
		return out
	case string:
		if opaqueKs[key] && t != "" {
			// Cursors embed random ids in base64; keep them opaque but stable.
			return base64.RawURLEncoding.EncodeToString([]byte(n.str(decodeOr(t))))
		}
		return n.str(t)
	}
	return v
}

func decodeOr(s string) string {
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return string(b)
	}
	return s
}
