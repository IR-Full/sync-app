package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
)

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

/*
The billing store.

The whole difficulty of a payment integration is in two places, and both are here
rather than in the service:

  - CreatePayment is idempotent by UNIQUE INDEX, not by a pre-SELECT. A
    check-then-insert leaves a window in which two concurrent retries both pass the
    check, and what gets duplicated is a charge.
  - ApplyPaymentStatus advances the payment and the subscription in ONE
    transaction, and validates the state transition against the STORED status inside
    it. Providers retry and reorder; without the check a pending notification
    arriving after a succeeded one un-pays a paid subscription.
*/

func (s *Store) GetSubscription(ctx context.Context, userID string) (*model.Subscription, error) {
	var (
		sub model.Subscription
		uid int64
	)
	err := s.reader().QueryRow(ctx,
		`SELECT user_id, plan, status, current_period_end, provider, cancel_at_period_end,
		        created_at, updated_at
		 FROM subscriptions WHERE user_id=$1`, atoi(userID)).
		Scan(&uid, &sub.Plan, &sub.Status, &sub.CurrentPeriodEnd, &sub.Provider,
			&sub.CancelAtPeriodEnd, &sub.CreatedAt, &sub.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sub.UserID = itoa(uid)
	return &sub, nil
}

func (s *Store) PutSubscription(ctx context.Context, sub *model.Subscription) error {
	_, err := s.pool.Exec(ctx, putSubscriptionSQL,
		atoi(sub.UserID), string(sub.Plan), string(sub.Status), sub.CurrentPeriodEnd,
		sub.Provider, sub.CancelAtPeriodEnd, sub.CreatedAt, sub.UpdatedAt)
	return wrap(err)
}

// CreatePayment inserts an attempt, or returns the existing one for a repeated
// idempotency key.
//
// The duplicate is resolved from the UNIQUE VIOLATION rather than avoided by a
// prior read: the read would be a second round trip that still races, and losing
// that race means charging someone twice.
func (s *Store) CreatePayment(ctx context.Context, p *model.Payment) (*model.Payment, bool, error) {
	var ref any
	if p.ProviderRef != "" {
		ref = p.ProviderRef
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO payments (id, user_id, plan, provider, provider_ref, method,
		                       amount_minor, currency, status, idempotency_key,
		                       created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		atoi(p.ID), atoi(p.UserID), string(p.Plan), p.Provider, ref, string(p.Method),
		p.AmountMinor, p.Currency, string(p.Status), p.IdempotencyKey,
		p.CreatedAt, p.UpdatedAt)
	if err == nil {
		return p, false, nil
	}
	if !isUniqueViolation(err) {
		return nil, false, wrap(err)
	}
	existing, e2 := s.getPaymentByIdem(ctx, p.UserID, p.IdempotencyKey)
	if e2 != nil {
		return nil, false, e2
	}
	return existing, true, nil
}

func (s *Store) getPaymentByIdem(ctx context.Context, userID, key string) (*model.Payment, error) {
	return s.scanPayment(ctx, `WHERE user_id=$1 AND idempotency_key=$2`, atoi(userID), key)
}

func (s *Store) GetPaymentByRef(ctx context.Context, provider, providerRef string) (*model.Payment, error) {
	return s.scanPayment(ctx, `WHERE provider=$1 AND provider_ref=$2`, provider, providerRef)
}

func (s *Store) scanPayment(ctx context.Context, where string, args ...any) (*model.Payment, error) {
	var (
		p       model.Payment
		id, uid int64
		ref     *string
	)
	err := s.reader().QueryRow(ctx,
		`SELECT id, user_id, plan, provider, provider_ref, method, amount_minor,
		        currency, status, idempotency_key, created_at, updated_at
		 FROM payments `+where, args...).
		Scan(&id, &uid, &p.Plan, &p.Provider, &ref, &p.Method, &p.AmountMinor,
			&p.Currency, &p.Status, &p.IdempotencyKey, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.ID, p.UserID = itoa(id), itoa(uid)
	if ref != nil {
		p.ProviderRef = *ref
	}
	return &p, nil
}

// AttachProviderRef records the acquirer reference once the charge call returns it.
//
// Separate from CreatePayment because the row must exist BEFORE the provider is
// called: a callback can arrive before the charge response does, and a webhook that
// finds no row has to either drop the money or invent an account for it.
func (s *Store) AttachProviderRef(ctx context.Context, paymentID, providerRef string) error {
	ct, err := s.pool.Exec(ctx,
		`UPDATE payments SET provider_ref=$2 WHERE id=$1`, atoi(paymentID), providerRef)
	if err != nil {
		return wrap(err)
	}
	if ct.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// ApplyPaymentStatus advances a payment and, when it settles, the subscription.
//
// One transaction, and the transition is validated against the row read INSIDE it
// with FOR UPDATE. That combination is the entire idempotency story for webhooks: a
// provider sends its callback several times and in whatever order the network
// produces, so the same notification arrives twice and an older one arrives after a
// newer one. Without the lock two concurrent callbacks both read pending and both
// activate; without the transition check a reordered pending un-pays a paid
// subscription.
//
// changed=false means the callback was not news, which is the normal outcome for a
// retry and must not be reported as an error — a provider that receives an error
// retries forever.
func (s *Store) ApplyPaymentStatus(ctx context.Context, provider, providerRef string, next model.PaymentStatus, at int64, sub *model.Subscription) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, wrap(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		id      int64
		current model.PaymentStatus
	)
	err = tx.QueryRow(ctx,
		`SELECT id, status FROM payments WHERE provider=$1 AND provider_ref=$2 FOR UPDATE`,
		provider, providerRef).Scan(&id, &current)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, store.ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if !current.CanTransitionTo(next) {
		return false, nil
	}
	if _, err := tx.Exec(ctx,
		`UPDATE payments SET status=$2, updated_at=$3 WHERE id=$1`, id, string(next), at); err != nil {
		return false, err
	}
	// The subscription moves in the SAME transaction. Separating them means a crash
	// between the two leaves a paid customer on the free tier, which is the failure
	// mode that generates support tickets rather than log lines.
	if sub != nil {
		if _, err := tx.Exec(ctx, putSubscriptionSQL,
			atoi(sub.UserID), string(sub.Plan), string(sub.Status), sub.CurrentPeriodEnd,
			sub.Provider, sub.CancelAtPeriodEnd, sub.CreatedAt, sub.UpdatedAt); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, wrap(err)
	}
	return true, nil
}

func (s *Store) ListPayments(ctx context.Context, userID string, limit int) ([]*model.Payment, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.reader().Query(ctx,
		`SELECT id, user_id, plan, provider, provider_ref, method, amount_minor,
		        currency, status, idempotency_key, created_at, updated_at
		 FROM payments WHERE user_id=$1 ORDER BY created_at DESC, id DESC LIMIT $2`,
		atoi(userID), limit)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	var out []*model.Payment
	for rows.Next() {
		var (
			p       model.Payment
			id, uid int64
			ref     *string
		)
		if err := rows.Scan(&id, &uid, &p.Plan, &p.Provider, &ref, &p.Method,
			&p.AmountMinor, &p.Currency, &p.Status, &p.IdempotencyKey,
			&p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.ID, p.UserID = itoa(id), itoa(uid)
		if ref != nil {
			p.ProviderRef = *ref
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

// ExpireSubscriptions moves lapsed rows to canceled and returns whose they were.
//
// The affected accounts are RETURNED rather than just counted, because their
// entitlements change the moment this runs and every connected device has to be
// told: a client still showing Premium after the period ends will offer features
// the server now refuses, which reads as the app breaking.
func (s *Store) ExpireSubscriptions(ctx context.Context, now int64, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx,
		`UPDATE subscriptions SET status='canceled', updated_at=$1
		 WHERE user_id IN (
		   SELECT user_id FROM subscriptions
		   WHERE status IN ('active','past_due')
		     AND current_period_end <> 0 AND current_period_end <= $1
		   ORDER BY current_period_end LIMIT $2
		 )
		 RETURNING user_id`, now, limit)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var uid int64
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		out = append(out, itoa(uid))
	}
	return out, rows.Err()
}
