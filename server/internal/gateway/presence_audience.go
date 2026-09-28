package gateway

import (
	"context"
	"errors"

	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
)

/*
The presence half of the privacy settings.

Fanout decides who hears that a user came online, and it has no reason to know
that accounts have settings — so it asks through a one-method interface and this
type answers, assembled from the user directory and the address book the gateway
already holds. The same shape as mediaAuthorizer, for the same reason.

Why it lives on the gateway rather than in fanout: "contacts" is a question about
two accounts, and answering it needs the user row and the contact row. Giving
fanout those dependencies would mean every deployment that routes presence also
has to wire a user store, including the ones that split the services apart.
*/
type presenceAudience struct {
	svc *Services
}

// NewPresenceAudience builds the gate fanout consults before announcing
// presence. Exported so cmd/server can wire it; a deployment that does not keeps
// the previous behaviour, where presence reaches every direct peer.
func NewPresenceAudience(svc Services) interface {
	MaySeePresence(ctx context.Context, ownerID, viewerID string) (bool, error)
} {
	return presenceAudience{svc: &svc}
}

func (p presenceAudience) MaySeePresence(ctx context.Context, ownerID, viewerID string) (bool, error) {
	if ownerID == viewerID {
		return true, nil
	}
	if p.svc.Users == nil {
		// Nothing to read the setting from. Allowing is the historical behaviour
		// and the honest one: a deployment without a user directory has no setting
		// to honour, so hiding presence would be inventing a policy nobody set.
		return true, nil
	}
	u, err := p.svc.Users.GetUser(ctx, ownerID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// The account is gone — there is no presence to announce and nobody to
			// announce it about.
			return false, nil
		}
		return false, err
	}

	switch model.NormalizeVisibility(u.Privacy.LastSeen) {
	case model.VisibilityEveryone:
		return true, nil
	case model.VisibilityNobody:
		return false, nil
	default:
		if p.svc.Contacts == nil {
			return false, nil
		}
		// The OWNER's address book decides. Asking it the other way round would let
		// anyone grant themselves a view by adding the person they want to watch.
		return p.svc.Contacts.IsContact(ctx, ownerID, viewerID)
	}
}
