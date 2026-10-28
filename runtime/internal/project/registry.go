package project

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/Mineru98/samsara/runtime/internal/domain"
)

// Registry is the local trust/activation registry. Only the daemon writes it;
// the hook bridge reads it to decide, without any socket connection or
// backend probe, whether a session is out of scope (R-01, ACT-01).
type Registry struct {
	Version         int             `json:"version"`
	Projects        []RegistryEntry `json:"projects"`
	ManagedRoots    []string        `json:"managed_roots"`
	ManagedSessions []string        `json:"managed_sessions"` // HashSecret(claude_session_id)
}

// RegistryEntry is one locally trusted project.
type RegistryEntry struct {
	ProjectID   string `json:"project_id"`
	Root        string `json:"root"`
	Activation  string `json:"activation"`
	DrainActive bool   `json:"drain_active"`
	Autostart   bool   `json:"autostart"`
}

// LoadRegistry reads the registry. A missing file is an empty registry.
func LoadRegistry(path string) (*Registry, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Registry{Version: 1}, nil
	}
	if err != nil {
		return nil, err
	}
	r := &Registry{}
	if err := json.Unmarshal(b, r); err != nil {
		return nil, err
	}
	return r, nil
}

// Save writes the registry atomically (temp file + rename) with 0600.
func (r *Registry) Save(path string) error {
	r.Version = 1
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".registry-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Scope is the hook's offline classification of a session.
type Scope struct {
	Entry          *RegistryEntry
	ManagedRoot    bool
	ManagedSession bool
}

// Relevant reports whether the hook must talk to the daemon at all.
func (s Scope) Relevant() bool {
	if s.ManagedRoot || s.ManagedSession {
		return true
	}
	if s.Entry == nil {
		return false
	}
	return s.Entry.Activation == domain.ActivationEnabled || s.Entry.DrainActive
}

// Managed reports whether the policy gate must fail closed.
func (s Scope) Managed() bool { return s.ManagedRoot || s.ManagedSession }

// Classify finds the deepest registered project containing cwd.
func (r *Registry) Classify(cwd, claudeSessionID string) Scope {
	var sc Scope
	best := -1
	for i := range r.Projects {
		e := &r.Projects[i]
		if Within(cwd, e.Root) && len(e.Root) > best {
			sc.Entry, best = e, len(e.Root)
		}
	}
	for _, m := range r.ManagedRoots {
		if Within(cwd, m) {
			sc.ManagedRoot = true
		}
	}
	if claudeSessionID != "" {
		h := domain.HashSecret(claudeSessionID)
		for _, s := range r.ManagedSessions {
			if s == h {
				sc.ManagedSession = true
			}
		}
	}
	return sc
}
