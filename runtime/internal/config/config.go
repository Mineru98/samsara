// Package config parses and validates the shareable repository policy file
// `.samsara/config.yaml` (01 §4.3). Unknown fields are errors. The file alone
// never authorises execution: local trust lives in the runtime registry.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mineru98/samsara/runtime/internal/domain"
	"gopkg.in/yaml.v3"
)

// RelPath is the repository-relative location of the policy file.
const RelPath = ".samsara/config.yaml"

// Config is the repository policy.
type Config struct {
	Version     int         `yaml:"version" json:"version"`
	Runtime     Runtime     `yaml:"runtime" json:"runtime"`
	Backend     Backend     `yaml:"backend" json:"backend"`
	Execution   Execution   `yaml:"execution" json:"execution"`
	Approval    Approval    `yaml:"approval" json:"approval"`
	Git         Git         `yaml:"git" json:"git"`
	Observation Observation `yaml:"observation" json:"observation"`
}

type Runtime struct {
	Enabled   bool `yaml:"enabled" json:"enabled"`
	Autostart bool `yaml:"autostart" json:"autostart"`
}

type Backend struct {
	Mode          string   `yaml:"mode" json:"mode"`
	Order         []string `yaml:"order" json:"order"`
	AllowFallback bool     `yaml:"allow_fallback" json:"allow_fallback"`
}

type Execution struct {
	MaxWorkers              int      `yaml:"max_workers" json:"max_workers"`
	MaxWorkersPerIssue      int      `yaml:"max_workers_per_issue" json:"max_workers_per_issue"`
	ManagedWorktreeRequired bool     `yaml:"managed_worktree_required" json:"managed_worktree_required"`
	UnknownWorkerPolicy     string   `yaml:"unknown_worker_policy" json:"unknown_worker_policy"`
	WorkerCommand           []string `yaml:"worker_command" json:"worker_command"`
	LeaseTTLSeconds         int      `yaml:"lease_ttl_seconds" json:"lease_ttl_seconds"`
	HeartbeatSeconds        int      `yaml:"heartbeat_seconds" json:"heartbeat_seconds"`
}

type Approval struct {
	ImplementationStart string `yaml:"implementation_start" json:"implementation_start"`
	Result              string `yaml:"result" json:"result"`
	Integration         string `yaml:"integration" json:"integration"`
}

type Git struct {
	TargetRef                 string `yaml:"target_ref" json:"target_ref"`
	IntegrationAuthority      string `yaml:"integration_authority" json:"integration_authority"`
	MergeConcurrencyPerTarget int    `yaml:"merge_concurrency_per_target" json:"merge_concurrency_per_target"`
	BranchPrefix              string `yaml:"branch_prefix" json:"branch_prefix"`
}

type Observation struct {
	CollectTerminalOutput bool `yaml:"collect_terminal_output" json:"collect_terminal_output"`
	EventRetentionDays    int  `yaml:"event_retention_days" json:"event_retention_days"`
}

// Default returns the documented defaults.
func Default() *Config {
	return &Config{
		Version: 1,
		Runtime: Runtime{Enabled: true, Autostart: true},
		Backend: Backend{Mode: "auto", Order: []string{domain.BackendHerdr, domain.BackendTmux, domain.BackendStandalone}, AllowFallback: true},
		Execution: Execution{MaxWorkers: 5, MaxWorkersPerIssue: 1, ManagedWorktreeRequired: true, UnknownWorkerPolicy: "quarantine",
			WorkerCommand: []string{"claude"}, LeaseTTLSeconds: 30, HeartbeatSeconds: 5},
		Approval:    Approval{ImplementationStart: "manual", Result: "manual", Integration: "manual"},
		Git:         Git{TargetRef: "refs/heads/main", IntegrationAuthority: domain.AuthorityManual, MergeConcurrencyPerTarget: 1, BranchPrefix: "samsara"},
		Observation: Observation{CollectTerminalOutput: false, EventRetentionDays: 7},
	}
}

// Parse decodes YAML over the defaults with strict field checking and
// validates the result.
func Parse(data []byte) (*Config, error) {
	cfg := Default()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, domain.Errf(domain.CodeInvalidRequest, "config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Load reads the policy file of a project root. A missing file yields
// (nil, nil); a malformed file is an error, never a silent DISABLED.
func Load(root string) (*Config, []byte, error) {
	b, err := os.ReadFile(filepath.Join(root, RelPath))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	cfg, err := Parse(b)
	return cfg, b, err
}

// Validate enforces value ranges and enums.
func (c *Config) Validate() error {
	var problems []string
	add := func(f string, a ...any) { problems = append(problems, fmt.Sprintf(f, a...)) }
	if c.Version != 1 {
		add("version must be 1")
	}
	switch c.Backend.Mode {
	case "auto", domain.BackendHerdr, domain.BackendTmux, domain.BackendStandalone:
	default:
		add("backend.mode %q is not one of auto|herdr|tmux|standalone", c.Backend.Mode)
	}
	seen := map[string]bool{}
	for _, b := range c.Backend.Order {
		switch b {
		case domain.BackendHerdr, domain.BackendTmux, domain.BackendStandalone:
		default:
			add("backend.order contains unknown backend %q", b)
		}
		if seen[b] {
			add("backend.order repeats %q", b)
		}
		seen[b] = true
	}
	if c.Execution.MaxWorkers < 1 {
		add("execution.max_workers must be >= 1")
	}
	if c.Execution.MaxWorkersPerIssue != 1 {
		add("execution.max_workers_per_issue must be 1 in this version")
	}
	if c.Execution.UnknownWorkerPolicy != "quarantine" {
		add("execution.unknown_worker_policy must be quarantine")
	}
	if len(c.Execution.WorkerCommand) == 0 || strings.TrimSpace(c.Execution.WorkerCommand[0]) == "" {
		add("execution.worker_command must name an executable")
	}
	if c.Execution.LeaseTTLSeconds < 5 {
		add("execution.lease_ttl_seconds must be >= 5")
	}
	if c.Execution.HeartbeatSeconds < 1 || c.Execution.HeartbeatSeconds*2 > c.Execution.LeaseTTLSeconds {
		add("execution.heartbeat_seconds must be >= 1 and at most half of lease_ttl_seconds")
	}
	for name, v := range map[string]string{"approval.implementation_start": c.Approval.ImplementationStart,
		"approval.result": c.Approval.Result, "approval.integration": c.Approval.Integration} {
		if v != "manual" && v != "auto" {
			add("%s must be manual|auto", name)
		}
	}
	if err := ValidateFullRef(c.Git.TargetRef); err != nil {
		add("git.target_ref: %v", err)
	}
	switch c.Git.IntegrationAuthority {
	case domain.AuthorityManual, domain.AuthorityGitHubQueue, domain.AuthoritySamsaraLocal:
	default:
		add("git.integration_authority %q is not one of manual|github_queue|samsara_local", c.Git.IntegrationAuthority)
	}
	if c.Git.MergeConcurrencyPerTarget != 1 {
		add("git.merge_concurrency_per_target must be 1")
	}
	if err := ValidateRefComponent(c.Git.BranchPrefix); err != nil {
		add("git.branch_prefix: %v", err)
	}
	if c.Observation.EventRetentionDays < 1 {
		add("observation.event_retention_days must be >= 1")
	}
	if len(problems) > 0 {
		return domain.Errf(domain.CodeInvalidRequest, "config invalid: %s", strings.Join(problems, "; ")).WithDetails("problems", problems)
	}
	return nil
}

// ValidateFullRef checks a full `refs/heads/...` ref with git's ref rules.
func ValidateFullRef(ref string) error {
	if !strings.HasPrefix(ref, "refs/heads/") {
		return fmt.Errorf("%q must be a full ref under refs/heads/", ref)
	}
	for _, part := range strings.Split(strings.TrimPrefix(ref, "refs/heads/"), "/") {
		if err := ValidateRefComponent(part); err != nil {
			return err
		}
	}
	return nil
}

// ValidateRefComponent rejects names git would refuse or that could be
// mistaken for options (04 §5.1).
func ValidateRefComponent(s string) error {
	if s == "" || s == "@" || strings.HasPrefix(s, ".") || strings.HasPrefix(s, "-") ||
		strings.HasSuffix(s, ".lock") || strings.HasSuffix(s, ".") || strings.Contains(s, "..") || strings.Contains(s, "@{") {
		return fmt.Errorf("invalid ref component %q", s)
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(" ~^:?*[\\", r) {
			return fmt.Errorf("invalid character %q in ref component %q", r, s)
		}
	}
	if len(s) > 100 {
		return fmt.Errorf("ref component too long")
	}
	return nil
}

// Marshal renders a config as YAML (used by `samsara init`).
func Marshal(c *Config) ([]byte, error) { return yaml.Marshal(c) }
