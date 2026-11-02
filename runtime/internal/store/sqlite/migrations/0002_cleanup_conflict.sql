-- 04 §12: Git-confirmed conflict classification, conflict resolution attempt
-- and supersession of a conflicted candidate by the resolved submission.
ALTER TABLE merge_candidates ADD COLUMN conflict_files TEXT NOT NULL DEFAULT '[]';
ALTER TABLE merge_candidates ADD COLUMN resolution_attempt_id TEXT;
ALTER TABLE merge_candidates ADD COLUMN superseded_by TEXT;
