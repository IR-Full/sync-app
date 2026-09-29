-- Platform-wide roles (admin, moderator), held in the database so they can be
-- granted and revoked without a redeploy. Environment-listed users are seeded in
-- at startup with granted_by = 'env'.
CREATE TABLE IF NOT EXISTS platform_roles (
    user_id    BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT   NOT NULL CHECK (role IN ('admin', 'moderator')),
    granted_by TEXT   NOT NULL,
    granted_at BIGINT NOT NULL
);
