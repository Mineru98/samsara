-- An attempt cancelled by a plan revision records the attempt that replaced
-- it, so history follows CONFLICT -> resolution -> PLAN_REVISED -> new attempt.
ALTER TABLE execution_attempts ADD COLUMN superseded_by TEXT;
