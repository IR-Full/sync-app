package memory

import (
	"context"
	"sort"

	"github.com/IR-Full/sync-app/server/internal/model"
	"github.com/IR-Full/sync-app/server/internal/store"
)

/*
The in-memory billing store.

It has to reproduce two properties the Postgres one gets from the database, and
both are the reason the conformance suite covers billing at all:

  - CreatePayment is idempotent on (user, idempotency key). There it is a unique
    index; here it is a map lookup under the same lock as the insert.
  - ApplyPaymentStatus validates the transition against the STORED status and moves
    the subscription atomically with the payment. There that is FOR UPDATE inside a
    transaction; here it is one critical section.

A backend that got either wrong would type-check identically and charge someone
twice, or leave a paid customer on the free tier.
*/

func (s *Store) GetSubscription(_ context.Context, userID string) (*model.Subscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sub, ok := s.subscriptions[userID]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *sub
	return &cp, nil
}

func (s *Store) PutSubscription(_ context.Context, sub *model.Subscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *sub
	s.subscriptions[sub.UserID] = &cp
	return nil
}

func (s *Store) CreatePayment(_ context.Context, p *model.Payment) (*model.Payment, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// The idempotency check and the insert are in ONE critical section. Splitting
	// them is the race that charges someone twice, and it is not a rare race — a
	// client retrying a timed-out checkout produces exactly two concurrent requests.
	key := p.UserID + "|" + p.IdempotencyKey
	if id, ok := s.paymentIdem[key]; ok {
		cp := *s.payments[id]
		return &cp, true, nil
	}
	cp := *p
	s.payments[p.ID] = &cp
	s.paymentIdem[key] = p.ID
	if p.ProviderRef != "" {
		s.paymentRefs[p.Provider+"|"+p.ProviderRef] = p.ID
	}
	out := cp
	return &out, false, nil
}

func (s *Store) GetPaymentByRef(_ context.Context, provider, providerRef string) (*model.Payment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.paymentRefs[provider+"|"+providerRef]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *s.payments[id]
	return &cp, nil
}

func (s *Store) AttachProviderRef(_ context.Context, paymentID, providerRef string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.payments[paymentID]
	if !ok {
		return store.ErrNotFound
	}
	if p.ProviderRef != "" {
		delete(s.paymentRefs, p.Provider+"|"+p.ProviderRef)
	}
	p.ProviderRef = providerRef
	s.paymentRefs[p.Provider+"|"+providerRef] = paymentID
	return nil
}

func (s *Store) ApplyPaymentStatus(_ context.Context, provider, providerRef string, next model.PaymentStatus, at int64, sub *model.Subscription) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.paymentRefs[provider+"|"+providerRef]
	if !ok {
		return false, store.ErrNotFound
	}
	p := s.payments[id]
	if !p.Status.CanTransitionTo(next) {
		// Not an error: a duplicate callback is the expected case, and reporting it as
		// a failure would make a provider retry forever.
		return false, nil
	}
	p.Status = next
	p.UpdatedAt = at
	// Atomic with the payment, for the same reason the Postgres one uses a
	// transaction: a gap between the two leaves a paid customer on the free tier.
	if sub != nil {
		cp := *sub
		s.subscriptions[sub.UserID] = &cp
	}
	return true, nil
}

func (s *Store) ListPayments(_ context.Context, userID string, limit int) ([]*model.Payment, error) {
	if limit <= 0 {
		limit = 50
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*model.Payment
	for _, p := range s.payments {
		if p.UserID != userID {
			continue
		}
		cp := *p
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].ID > out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Store) ExpireSubscriptions(_ context.Context, now int64, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 500
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for uid, sub := range s.subscriptions {
		if len(out) >= limit {
			break
		}
		live := sub.Status == model.SubActive || sub.Status == model.SubPastDue
		if !live || sub.CurrentPeriodEnd == 0 || sub.CurrentPeriodEnd > now {
			continue
		}
		sub.Status = model.SubCanceled
		sub.UpdatedAt = now
		out = append(out, uid)
	}
	return out, nil
}
