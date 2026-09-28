package postgres

// putSubscriptionSQL is the subscription upsert, shared by PutSubscription and by
// ApplyPaymentStatus.
//
// Shared deliberately: the second runs inside the payment transaction, and two
// separately-maintained copies of this statement would eventually disagree about
// which columns a settled payment updates — with the disagreement showing up as a
// paid customer on the wrong tier.
const putSubscriptionSQL = `
INSERT INTO subscriptions (user_id, plan, status, current_period_end, provider,
                           cancel_at_period_end, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (user_id) DO UPDATE SET
  plan = EXCLUDED.plan,
  status = EXCLUDED.status,
  current_period_end = EXCLUDED.current_period_end,
  provider = EXCLUDED.provider,
  cancel_at_period_end = EXCLUDED.cancel_at_period_end,
  updated_at = EXCLUDED.updated_at`
