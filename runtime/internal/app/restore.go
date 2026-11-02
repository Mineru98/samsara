package app

import (
	"context"

	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

// PrepareRestored runs once on a database that was just restored from a
// backup, before any daemon opens it (05 §17.3, DB-12, DB-13, EVT-11):
//   - every in-flight intent of the backup becomes UNKNOWN. Facts that
//     happened after the backup are not in it, so nothing is re-sent
//     blindly; intents whose handler inspects external state first are
//     re-queued by reconcileUnknownOutbox, the rest wait for a person.
//   - PUBLISHING candidates must re-read the real target before anything
//     else (reconcilePublishing), never assumed unpublished.
//   - the database generation rotates, so every cursor issued before the
//     restore gets reset_required instead of a silent gap.
func PrepareRestored(ctx context.Context, db *sqlite.DB, source string) error {
	before := db.DatabaseGeneration
	err := db.Write(ctx, func(tx *sqlite.Tx) error {
		inflight := "SELECT operation_id FROM outbox WHERE state IN ('PENDING','RUNNING')"
		stmts := []struct {
			sql  string
			args []any
		}{
			{"UPDATE operations SET state='UNKNOWN', updated_at=? WHERE id IN (" + inflight + ")", []any{tx.Now}},
			{"UPDATE dispatches SET state='UNKNOWN' WHERE state IN ('QUEUED','SENT') AND operation_id IN (" + inflight + ")", nil},
			{"UPDATE outbox SET state='UNKNOWN', last_error='restored from backup: outcome must be reconciled', updated_at=? WHERE state IN ('PENDING','RUNNING')", []any{tx.Now}},
			{"UPDATE merge_candidates SET publication_observation='UNKNOWN' WHERE state='PUBLISHING'", nil},
		}
		for _, s := range stmts {
			if _, err := tx.ExecContext(ctx, s.sql, s.args...); err != nil {
				return err
			}
		}
		return tx.Audit(ctx, "", "cli", "runtime.restore", "database", before, "ALLOW", "RESTORED_FROM_BACKUP", map[string]string{"source": source})
	})
	if err != nil {
		return err
	}
	return db.RotateDatabaseGeneration(ctx)
}
