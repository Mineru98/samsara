// Package hooks installs Samsara's Claude Code hook entries and parses hook
// payloads. Installation only adds managed entries to
// `.claude/settings.local.json` and preserves every user entry (ACT-11).
package hooks

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// SettingsRel is the project-local, uncommitted Claude settings file.
const SettingsRel = ".claude/settings.local.json"

// MinBlockVersion is the first Claude Code version documented to support
// `onFailure: "block"` for command hooks (01 §10.5).
const MinBlockVersion = "2.1.295"

// Events maps Claude hook event names to bridge sub-commands.
var Events = []struct {
	Name, Sub string
	Timeout   int
	Block     bool
	Matcher   bool
}{
	{"SessionStart", "session-start", 10, false, false},
	{"UserPromptSubmit", "user-prompt-submit", 3, false, false},
	{"PreToolUse", "pre-tool-use", 3, true, true},
	{"PostToolUse", "post-tool-use", 3, false, true},
	{"PostToolUseFailure", "post-tool-use-failure", 3, false, true},
	{"Notification", "notification", 3, false, false},
	{"Stop", "stop", 3, false, false},
	{"SessionEnd", "session-end", 3, false, false},
	{"SubagentStart", "subagent-start", 3, false, false},
	{"SubagentStop", "subagent-stop", 3, false, false},
}

func isOurs(h map[string]any) bool {
	args, _ := h["args"].([]any)
	cmd, _ := h["command"].(string)
	if len(args) == 0 {
		return false
	}
	first, _ := args[0].(string)
	return first == "hook" && filepath.Base(cmd) == "samsara"
}

func readSettings(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	if len(strings.TrimSpace(string(b))) == 0 {
		return m, nil
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// Install adds managed hook entries for exe (an absolute path) into
// dir/.claude/settings.local.json, replacing older Samsara entries only.
func Install(dir, exe string) error {
	if !filepath.IsAbs(exe) {
		return errors.New("hook executable path must be absolute")
	}
	path := filepath.Join(dir, SettingsRel)
	m, err := readSettings(path)
	if err != nil {
		return err
	}
	hooksMap, _ := m["hooks"].(map[string]any)
	if hooksMap == nil {
		hooksMap = map[string]any{}
	}
	for _, ev := range Events {
		groups, _ := hooksMap[ev.Name].([]any)
		var kept []any
		for _, g := range groups {
			gm, ok := g.(map[string]any)
			if !ok {
				kept = append(kept, g)
				continue
			}
			hs, _ := gm["hooks"].([]any)
			var keepHooks []any
			for _, h := range hs {
				if hm, ok := h.(map[string]any); ok && isOurs(hm) {
					continue
				}
				keepHooks = append(keepHooks, h)
			}
			if len(keepHooks) == 0 && len(hs) > 0 {
				continue
			}
			gm["hooks"] = keepHooks
			kept = append(kept, gm)
		}
		entry := map[string]any{"type": "command", "command": exe, "args": []any{"hook", ev.Sub}, "timeout": ev.Timeout}
		if ev.Block {
			entry["onFailure"] = "block"
		}
		group := map[string]any{"hooks": []any{entry}}
		if ev.Matcher {
			group["matcher"] = ".*"
		}
		hooksMap[ev.Name] = append(kept, group)
	}
	m["hooks"] = hooksMap
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".samsara-tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// PolicyHookInstalled reports whether dir's settings contain the blocking
// PreToolUse entry with an existing executable.
func PolicyHookInstalled(dir string) (bool, error) {
	m, err := readSettings(filepath.Join(dir, SettingsRel))
	if err != nil {
		return false, err
	}
	hooksMap, _ := m["hooks"].(map[string]any)
	groups, _ := hooksMap["PreToolUse"].([]any)
	for _, g := range groups {
		gm, _ := g.(map[string]any)
		hs, _ := gm["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if hm == nil || !isOurs(hm) || hm["onFailure"] != "block" {
				continue
			}
			if async, _ := hm["async"].(bool); async {
				return false, nil // async hooks are never a gate (HK-09)
			}
			cmd, _ := hm["command"].(string)
			if fi, err := os.Stat(cmd); err == nil && fi.Mode()&0o111 != 0 {
				return true, nil
			}
		}
	}
	return false, nil
}

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// ParseVersion extracts major.minor.patch from `claude --version` output.
func ParseVersion(s string) (string, bool) {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	return m[1] + "." + m[2] + "." + m[3], true
}

// VersionAtLeast compares dotted versions.
func VersionAtLeast(v, min string) bool {
	a, b := strings.Split(v, "."), strings.Split(min, ".")
	for i := 0; i < 3; i++ {
		var x, y int
		if i < len(a) {
			x, _ = strconv.Atoi(a[i])
		}
		if i < len(b) {
			y, _ = strconv.Atoi(b[i])
		}
		if x != y {
			return x > y
		}
	}
	return true
}
