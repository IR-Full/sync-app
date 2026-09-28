DROP INDEX IF EXISTS idx_sessions_prev_resume;
ALTER TABLE sessions
  DROP COLUMN IF EXISTS prev_resume_token,
  DROP COLUMN IF EXISTS resume_rotated_at;
DROP TABLE IF EXISTS user_recovery_codes;
DROP TABLE IF EXISTS user_two_factor;
