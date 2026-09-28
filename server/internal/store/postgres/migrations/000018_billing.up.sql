-- Subscriptions and payments.
--
-- Money is stored in MINOR UNITS as BIGINT — kopeks, cents — never as a float or a
-- NUMERIC read into one. A price is an exact quantity, binary floating point cannot
-- represent 0.01, and the error compounds across a ledger until somebody discovers
-- it in a reconciliation.

CREATE TABLE IF NOT EXISTS subscriptions (
    user_id             BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    plan                TEXT   NOT NULL,
    status              TEXT   NOT NULL,
    -- When access lapses without a renewal.
    current_period_end  BIGINT NOT NULL DEFAULT 0,
    -- Which acquirer holds the instrument, so a renewal or refund goes back to the
    -- one that took the money.
    provider            TEXT   NOT NULL DEFAULT '',
    -- A cancellation that has not taken effect yet. Cancelling does not revoke
    -- access: the period is paid for, and taking away what was bought the moment
    -- someone clicks cancel teaches them not to click it.
    cancel_at_period_end BOOLEAN NOT NULL DEFAULT FALSE,
    created_at          BIGINT NOT NULL,
    updated_at          BIGINT NOT NULL
);

-- The expiry sweep reads this.
CREATE INDEX IF NOT EXISTS idx_subscriptions_period
    ON subscriptions(current_period_end)
    WHERE status IN ('active', 'past_due');

CREATE TABLE IF NOT EXISTS payments (
    id              BIGINT PRIMARY KEY,          -- snowflake
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    plan            TEXT   NOT NULL,
    provider        TEXT   NOT NULL,
    -- The provider's own id. NULL until the charge call returns, because the row has
    -- to exist BEFORE the provider is called: a callback can arrive before the
    -- charge response does, and a webhook that finds no row has to either drop the
    -- money or invent an account for it.
    provider_ref    TEXT,
    method          TEXT   NOT NULL,
    amount_minor    BIGINT NOT NULL,
    currency        TEXT   NOT NULL,
    status          TEXT   NOT NULL,
    -- The CLIENT's idempotency key, so a retried checkout resolves to this row
    -- instead of starting a second payment. Same reasoning as a message dedup key,
    -- with worse consequences for getting it wrong.
    idempotency_key TEXT   NOT NULL,
    created_at      BIGINT NOT NULL,
    updated_at      BIGINT NOT NULL
);

-- Idempotency, enforced by the database rather than by a pre-SELECT. A
-- check-then-insert leaves a window in which two concurrent retries both pass the
-- check, and the thing that gets duplicated is a charge.
CREATE UNIQUE INDEX IF NOT EXISTS uq_payments_idem
    ON payments(user_id, idempotency_key);

-- Callback deduplication. A provider sends its notification more than once, so the
-- lookup from (provider, provider_ref) to a payment has to be unique AND indexed:
-- unique because two rows for one provider payment cannot both be right, indexed
-- because every webhook does this lookup.
CREATE UNIQUE INDEX IF NOT EXISTS uq_payments_provider_ref
    ON payments(provider, provider_ref)
    WHERE provider_ref IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_payments_user ON payments(user_id, created_at DESC);
