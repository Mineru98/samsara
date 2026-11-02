// Package diag builds the `samsara doctor --export` bundle (01 §18, R5
// diagnostics export). The bundle carries metadata and state only: tokens,
// prompts, transcripts and tool input are removed by default, by field name
// and by known credential patterns, before anything is written.
package diag

import (
	"bufio"
	"encoding/json"
	"os"
	"regexp"
	"strings"
)

// Redacted replaces every removed value.
const Redacted = "[REDACTED]"

// sensitiveKey matches field names whose values are never exported.
var sensitiveKey = regexp.MustCompile(`(?i)(token|secret|passw|credential|authorization|cookie|api[_-]?key|private[_-]?key|prompt|transcript|tool_input|tool_response|bootstrap|^code$|managed_sessions|payload|ticket|bash_commands|^command$|argv)`)

// secretPatterns match credentials inside free text (log lines, errors).
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{10,}`),
	regexp.MustCompile(`xox[abprs]-[A-Za-z0-9\-]{10,}`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=\-]{8,}`),
	regexp.MustCompile(`(?i)\b(token|secret|password|passwd|api[_-]?key)\s*[=:]\s*[^\s"',;]+`),
	regexp.MustCompile(`(?i)"(token|secret|password|passwd|api[_-]?key)"\s*:\s*"[^"]*"`),
	regexp.MustCompile(`://[^/\s:@]+:[^@\s/]+@`),
}

// Redactor removes sensitive values and counts what it removed.
type Redactor struct{ Count int }

// String scrubs credential patterns from free text.
func (r *Redactor) String(s string) string {
	for _, re := range secretPatterns {
		s = re.ReplaceAllStringFunc(s, func(string) string {
			r.Count++
			return Redacted
		})
	}
	return s
}

// Value returns a redacted copy of a JSON-like value (maps, slices,
// strings, numbers). Values of sensitive keys are replaced entirely.
func (r *Redactor) Value(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			if sensitiveKey.MatchString(k) && !empty(x) {
				r.Count++
				out[k] = Redacted
				continue
			}
			out[k] = r.Value(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = r.Value(x)
		}
		return out
	case string:
		return r.String(t)
	default:
		return v
	}
}

func empty(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case bool:
		return !t
	case []any:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	}
	return false
}

// Any converts a Go value to its JSON form and redacts it.
func (r *Redactor) Any(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return r.String(err.Error())
	}
	var g any
	if err := json.Unmarshal(b, &g); err != nil {
		return r.String(string(b))
	}
	return r.Value(g)
}

// LogTail returns the last n lines of a log file, redacted. JSON log lines
// are redacted by field; other lines by pattern.
func (r *Redactor) LogTail(path string, n int) []any {
	f, err := os.Open(path)
	if err != nil {
		return []any{}
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	out := make([]any, 0, len(lines))
	for _, l := range lines {
		var m map[string]any
		if strings.HasPrefix(strings.TrimSpace(l), "{") && json.Unmarshal([]byte(l), &m) == nil {
			out = append(out, r.Value(m))
			continue
		}
		out = append(out, r.String(l))
	}
	return out
}

// Policy describes what the export leaves out; it is part of the bundle.
const Policy = "metadata and state only. Values of fields named like token, secret, password, credential, authorization, cookie, api key, prompt, transcript, tool input/response, payload and bootstrap code are replaced with [REDACTED]; GitHub/Anthropic/Slack/AWS credentials and bearer values are scrubbed from free text. Terminal output, prompts and event payloads are not collected."
