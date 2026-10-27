package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/Mineru98/samsara/runtime/internal/config"
	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/project"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

// RegisterProjectRequest is sent by `samsara init` / `samsara enable`.
type RegisterProjectRequest struct {
	Path string `json:"path"`
}

// RegisterProjectResult reports the registered project.
type RegisterProjectResult struct {
	ProjectID  string `json:"project_id"`
	Root       string `json:"root"`
	Activation string `json:"activation"`
	Created    bool   `json:"created"`
	TargetID   string `json:"target_id,omitempty"`
}

// RegisterProject records local trust for a project root and enables it. The
// repository config is read from disk and validated; a malformed config is an
// error, never a silent DISABLED (01 §4.2).
func (a *App) RegisterProject(ctx context.Context, cmd Cmd, req RegisterProjectRequest) (*RegisterProjectResult, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	id, err := project.Resolve(ctx, req.Path)
	if err != nil {
		return nil, domain.Errf(domain.CodeInvalidRequest, "cannot resolve %s: %v", req.Path, err)
	}
	cfg, raw, err := config.Load(id.Root)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		cfg = config.Default()
	}
	cfgJSON, _ := json.Marshal(cfg)
	digest := domain.DigestBytes(raw)
	res := &RegisterProjectResult{Root: id.Root, Activation: domain.ActivationEnabled}
	err = a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		p, err := sqlite.GetProjectByRoot(ctx, tx, id.Root)
		if err != nil && !domain.IsCode(err, domain.CodeNotFound) {
			return err
		}
		if p == nil {
			res.Created = true
			p = &sqlite.Project{ID: domain.NewID("prj")}
			if _, err := tx.ExecContext(ctx, `INSERT INTO projects(id,root_path,display_name,activation,config_digest,config_json,created_at,updated_at,last_activity_at)
				VALUES(?,?,?,?,?,?,?,?,?)`, p.ID, id.Root, filepath.Base(id.Root), domain.ActivationEnabled, digest, string(cfgJSON), tx.Now, tx.Now, tx.Now); err != nil {
				return err
			}
		} else {
			if p.DrainState == domain.DrainRequested || p.DrainState == domain.DrainDraining {
				return domain.Errf(domain.CodeConflict, "project is still draining; wait for drain to finish before re-enabling")
			}
			policyBump := int64(0)
			if p.ConfigDigest != digest {
				policyBump = 1
			}
			if _, err := tx.ExecContext(ctx, `UPDATE projects SET activation='ENABLED', registry_state='PRESENT', drain_state='NONE', config_digest=?, config_json=?,
				policy_version=policy_version+?, version=version+1, updated_at=? WHERE id=?`, digest, string(cfgJSON), policyBump, tx.Now, p.ID); err != nil {
				return err
			}
		}
		res.ProjectID = p.ID
		if id.IsGit {
			repoID, err := ensureRepository(ctx, tx, id)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO project_repositories(project_id,repository_id) VALUES(?,?)", p.ID, repoID); err != nil {
				return err
			}
			tid, err := ensureTarget(ctx, tx, repoID, cfg.Git.TargetRef, cfg.Git.IntegrationAuthority, id.CommonDir)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO project_targets(project_id,target_id) VALUES(?,?)", p.ID, tid); err != nil {
				return err
			}
			res.TargetID = tid
			tx.Touch(p.ID, sqlite.KindTarget, tid)
		}
		tx.Touch(p.ID, sqlite.KindProject, p.ID)
		tx.Event(p.ID, "project.enabled", "project", p.ID, 0)
		return tx.Audit(ctx, p.ID, cmd.Principal.ID, "project.enable", "project", p.ID, "", "", map[string]any{"root": id.Root, "config_digest": digest})
	})
	if err != nil {
		return nil, err
	}
	return res, a.SyncRegistry(ctx)
}

func ensureRepository(ctx context.Context, tx *sqlite.Tx, id *project.Identity) (string, error) {
	var repoID string
	err := tx.QueryRowContext(ctx, "SELECT id FROM repositories WHERE common_dir=?", id.CommonDir).Scan(&repoID)
	if errors.Is(err, sql.ErrNoRows) {
		repoID = domain.NewID("repo")
		_, err = tx.ExecContext(ctx, "INSERT INTO repositories(id,common_dir,object_format,remote_identity,created_at) VALUES(?,?,?,?,?)",
			repoID, id.CommonDir, id.ObjectFormat, id.RemoteIdentity, tx.Now)
	}
	return repoID, err
}

// ensureTarget finds or creates the integration target. One authority per
// (repository, target_ref) across all projects (INV-10).
func ensureTarget(ctx context.Context, tx *sqlite.Tx, repoID, ref, authority, integrationRepo string) (string, error) {
	t, err := sqlite.FindTarget(ctx, tx, repoID, ref)
	if err != nil {
		return "", err
	}
	if t != nil {
		if t.Authority != authority {
			return "", domain.Errf(domain.CodeConflict, "target %s already uses authority %s; a target has exactly one integration authority", ref, t.Authority).
				WithDetails("target_id", t.ID, "existing_authority", t.Authority, "requested_authority", authority)
		}
		return t.ID, nil
	}
	id := domain.NewID("tgt")
	_, err = tx.ExecContext(ctx, "INSERT INTO integration_targets(id,repository_id,target_ref,authority,integration_repo,created_at) VALUES(?,?,?,?,?,?)",
		id, repoID, ref, authority, integrationRepo, tx.Now)
	if err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO target_locks(target_id,current_fence) VALUES(?,0)", id)
	return id, err
}

// DisableProject stops new work and drains managed attempts (01 §4.4). The
// guard of already managed sessions stays until drain completes.
func (a *App) DisableProject(ctx context.Context, cmd Cmd, projectID string) (*sqlite.ProjectView, error) {
	if err := a.requireReady(); err != nil {
		return nil, err
	}
	var out sqlite.ProjectView
	err := a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		return a.idempotent(ctx, tx, "project.disable:"+projectID, cmd, projectID, &out, func() (any, error) {
			p, err := sqlite.GetProject(ctx, tx, projectID)
			if err != nil {
				return nil, err
			}
			if err := checkIfMatch(cmd, p.Version); err != nil {
				return nil, err
			}
			if p.Activation == domain.ActivationDisabled && (p.DrainState == domain.DrainComplete || p.DrainState == domain.DrainNone) {
				return sqlite.ProjectProjection(p), nil
			}
			if _, err := tx.ExecContext(ctx, "UPDATE projects SET activation='DISABLED', drain_state='DRAINING', version=version+1, updated_at=? WHERE id=?", tx.Now, p.ID); err != nil {
				return nil, err
			}
			runs, err := sqlite.RunsInState(ctx, tx, domain.RunActive)
			if err != nil {
				return nil, err
			}
			for _, r := range runs {
				if r.ProjectID == p.ID {
					if err := a.pauseRunTx(ctx, tx, r, "DISABLE_DRAINING"); err != nil {
						return nil, err
					}
				}
			}
			tx.Touch(p.ID, sqlite.KindProject, p.ID)
			tx.Event(p.ID, "project.disable_requested", "project", p.ID, p.Version+1)
			if err := tx.Audit(ctx, p.ID, cmd.Principal.ID, "project.disable", "project", p.ID, "", "", nil); err != nil {
				return nil, err
			}
			np, err := sqlite.GetProject(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			return sqlite.ProjectProjection(np), nil
		})
	})
	if err != nil {
		return nil, err
	}
	a.Wake()
	return &out, a.SyncRegistry(ctx)
}

// progressDrains completes drains whose managed work has quiesced.
func (a *App) progressDrains(ctx context.Context) error {
	changed := false
	err := a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT id, updated_at FROM projects WHERE drain_state IN ('REQUESTED','DRAINING','NEEDS_ATTENTION')")
		if err != nil {
			return err
		}
		type pr struct {
			id      string
			updated int64
		}
		var ps []pr
		for rows.Next() {
			var x pr
			if err := rows.Scan(&x.id, &x.updated); err != nil {
				rows.Close()
				return err
			}
			ps = append(ps, x)
		}
		rows.Close()
		for _, p := range ps {
			var busy, leases int
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM execution_attempts WHERE project_id=? AND state IN ('STARTING','RUNNING','PAUSING','CANCEL_REQUESTED')", p.id).Scan(&busy); err != nil {
				return err
			}
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM leases WHERE project_id=? AND state='ACTIVE'", p.id).Scan(&leases); err != nil {
				return err
			}
			next := ""
			switch {
			case busy == 0 && leases == 0:
				next = domain.DrainComplete
			case tx.Now-p.updated > 10*60*1000:
				next = domain.DrainNeedsAttention
			}
			if next == "" {
				continue
			}
			var cur string
			_ = tx.QueryRowContext(ctx, "SELECT drain_state FROM projects WHERE id=?", p.id).Scan(&cur)
			if cur == next {
				continue
			}
			if next == domain.DrainComplete {
				// Release managed bindings: the project leaves management.
				if _, err := tx.ExecContext(ctx, "UPDATE session_assignments SET released_at=? WHERE released_at IS NULL AND session_id IN (SELECT id FROM sessions WHERE project_id=?)", tx.Now, p.id); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, "UPDATE sessions SET managed=0, version=version+1 WHERE project_id=? AND managed=1", p.id); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, "UPDATE projects SET drain_state=?, version=version+1 WHERE id=?", next, p.id); err != nil {
				return err
			}
			tx.Touch(p.id, sqlite.KindProject, p.id)
			tx.Event(p.id, "project.drain_"+next, "project", p.id, 0)
			changed = true
		}
		return nil
	})
	if err == nil && changed {
		err = a.SyncRegistry(ctx)
	}
	return err
}

// ArchiveProject hides a project from default views but keeps history.
func (a *App) ArchiveProject(ctx context.Context, cmd Cmd, projectID string) error {
	return a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		p, err := sqlite.GetProject(ctx, tx, projectID)
		if err != nil {
			return err
		}
		if p.Activation == domain.ActivationEnabled {
			return domain.Errf(domain.CodeConflict, "disable the project before archiving it")
		}
		if _, err := tx.ExecContext(ctx, "UPDATE projects SET registry_state='ARCHIVED', version=version+1 WHERE id=?", p.ID); err != nil {
			return err
		}
		tx.Touch(p.ID, sqlite.KindProject, p.ID)
		tx.Event(p.ID, "project.archived", "project", p.ID, 0)
		return tx.Audit(ctx, p.ID, cmd.Principal.ID, "project.archive", "project", p.ID, "", "", nil)
	})
}

// RelinkProject moves a project to a new root after comparing repository
// fingerprints (01 §4.1). It never merges two projects implicitly.
func (a *App) RelinkProject(ctx context.Context, cmd Cmd, projectID, newPath string) error {
	id, err := project.Resolve(ctx, newPath)
	if err != nil {
		return domain.Errf(domain.CodeInvalidRequest, "%v", err)
	}
	return a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		p, err := sqlite.GetProject(ctx, tx, projectID)
		if err != nil {
			return err
		}
		repo, err := sqlite.RepositoryOfProject(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		if repo != nil && repo.RemoteIdentity != "" && repo.RemoteIdentity != id.RemoteIdentity {
			return domain.Errf(domain.CodeConflict, "repository fingerprint differs (%s vs %s)", repo.RemoteIdentity, id.RemoteIdentity)
		}
		if other, _ := sqlite.GetProjectByRoot(ctx, tx, id.Root); other != nil && other.ID != p.ID {
			return domain.Errf(domain.CodeConflict, "path already registered as project %s", other.ID)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE projects SET root_path=?, registry_state='PRESENT', version=version+1 WHERE id=?", id.Root, p.ID); err != nil {
			return err
		}
		if repo != nil && id.IsGit {
			if _, err := tx.ExecContext(ctx, "UPDATE repositories SET common_dir=? WHERE id=?", id.CommonDir, repo.ID); err != nil {
				return err
			}
		}
		tx.Touch(p.ID, sqlite.KindProject, p.ID)
		tx.Event(p.ID, "project.relinked", "project", p.ID, 0)
		return tx.Audit(ctx, p.ID, cmd.Principal.ID, "project.relink", "project", p.ID, "", "", map[string]string{"from": p.RootPath, "to": id.Root})
	})
}

// markMissingProjects flags registered roots that vanished from disk. It
// never recreates paths or starts sessions (ACT-09).
func (a *App) markMissingProjects(ctx context.Context) error {
	var missing, present []string
	if err := a.DB.Read(ctx, func(q sqlite.Q) error {
		ps, err := sqlite.ListProjects(ctx, q)
		if err != nil {
			return err
		}
		for _, p := range ps {
			if p.RegistryState == domain.RegistryArchived {
				continue
			}
			_, err := os.Stat(p.RootPath)
			if errors.Is(err, os.ErrNotExist) && p.RegistryState != domain.RegistryMissing {
				missing = append(missing, p.ID)
			} else if err == nil && p.RegistryState == domain.RegistryMissing {
				present = append(present, p.ID)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if len(missing)+len(present) == 0 {
		return nil
	}
	return a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		for _, id := range missing {
			if _, err := tx.ExecContext(ctx, "UPDATE projects SET registry_state='MISSING', version=version+1 WHERE id=?", id); err != nil {
				return err
			}
			tx.Touch(id, sqlite.KindProject, id)
			tx.Event(id, "project.missing", "project", id, 0)
		}
		for _, id := range present {
			if _, err := tx.ExecContext(ctx, "UPDATE projects SET registry_state='PRESENT', version=version+1 WHERE id=?", id); err != nil {
				return err
			}
			tx.Touch(id, sqlite.KindProject, id)
			tx.Event(id, "project.present", "project", id, 0)
		}
		return nil
	})
}

// CheckProfileRequest registers a trusted verification command.
type CheckProfileRequest struct {
	ProjectID  string   `json:"project_id"`
	Name       string   `json:"name"`
	Argv       []string `json:"argv"`
	TimeoutSec int64    `json:"timeout_sec"`
}

// RegisterCheckProfile stores a trusted check definition. Profiles are the
// only commands Samsara executes for verification (PLN-05).
func (a *App) RegisterCheckProfile(ctx context.Context, cmd Cmd, req CheckProfileRequest) error {
	if err := config.ValidateRefComponent(req.Name); err != nil || len(req.Argv) == 0 {
		return domain.Errf(domain.CodeInvalidRequest, "profile needs a simple name and a non-empty argv")
	}
	if req.TimeoutSec <= 0 {
		req.TimeoutSec = 600
	}
	b, _ := json.Marshal(req.Argv)
	return a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		p, err := sqlite.GetProject(ctx, tx, req.ProjectID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO check_profiles(project_id,name,argv_json,timeout_sec,created_at) VALUES(?,?,?,?,?)
			ON CONFLICT(project_id,name) DO UPDATE SET argv_json=excluded.argv_json, timeout_sec=excluded.timeout_sec`,
			p.ID, req.Name, string(b), req.TimeoutSec, tx.Now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE projects SET policy_version=policy_version+1, version=version+1 WHERE id=?", p.ID); err != nil {
			return err
		}
		tx.Touch(p.ID, sqlite.KindProject, p.ID)
		tx.Event(p.ID, "project.check_profile_registered", "project", p.ID, 0)
		return tx.Audit(ctx, p.ID, cmd.Principal.ID, "check_profile.register", "project", p.ID, "", "", map[string]any{"name": req.Name, "argv": req.Argv})
	})
}
