ALTER TABLE users
  DROP COLUMN IF EXISTS privacy_last_seen,
  DROP COLUMN IF EXISTS privacy_avatar,
  DROP COLUMN IF EXISTS privacy_groups;
