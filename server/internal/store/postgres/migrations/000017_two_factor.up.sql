-- Second factor, and the resume-token rotation that goes with it.
--
-- Until now the first factor was the only one, and it could not even be changed:
-- a leaked password meant a permanently lost account, because revoking sessions
-- does not stop whoever knows the password from signing in again.
CREATE TABLE IF NOT EXISTS user_two_factor (
    user_id      BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    -- The TOTP shared secret, ENCRYPTED (AES-GCM, key from the environment).
    -- Not hashed: verification has to recompute a code, so it needs the secret
    -- itself. That is what makes the key's location — outside this database —
    -- the thing standing between a dump and everyone's second factor.
    secret_enc   TEXT   NOT NULL,
    -- 0 until the user has proved they can produce a code. A secret exists from
    -- the moment setup begins and is not ENFORCED before this, so a mis-scanned
    -- QR code cannot lock an account out of itself.
    confirmed_at BIGINT NOT NULL DEFAULT 0,
    created_at   BIGINT NOT NULL
);

-- Recovery codes, one row each so spending one is a DELETE rather than a
-- read-modify-write of an array. Single-use codes and check-then-write do not
-- mix: two concurrent logins would both spend the same code.
CREATE TABLE IF NOT EXISTS user_recovery_codes (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- argon2id hash. Hashed, not encrypted: unlike the TOTP secret these are
    -- never needed back, so a leak of this table yields nothing usable.
    code_hash TEXT NOT NULL,
    PRIMARY KEY (user_id, code_hash)
);

-- Resume-token rotation.
--
-- A resume token used to survive unchanged for the session's whole 14 days, so a
-- token captured once granted access for a fortnight and its use was
-- undetectable — the real client kept working alongside the thief.
--
-- prev_resume_token is the token that was just rotated away. Keeping it is what
-- makes theft VISIBLE: a resume arriving for an already-consumed token means two
-- parties hold the chain, and the safe reading of that is that one of them stole
-- it. The response is to kill the chain, not to guess which is which.
ALTER TABLE sessions
  ADD COLUMN IF NOT EXISTS prev_resume_token TEXT,
  ADD COLUMN IF NOT EXISTS resume_rotated_at BIGINT NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_sessions_prev_resume
  ON sessions(prev_resume_token) WHERE prev_resume_token IS NOT NULL;
