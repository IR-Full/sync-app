package gateway

import (
	"context"

	"github.com/IR-Full/sync-app/server/internal/store"
)

/*
The push half of the privacy settings.

A notification's endpoint is Apple's or Google's, so a message preview in it hands
a third party the text — for every conversation on the system, a wider disclosure
than anything end-to-end encryption protects against, since E2E guards against the
server and this is the server volunteering the plaintext.

Previews are not removed: people want them, and on a locked screen a notification
with no content is nearly useless. They are a choice, default off, made by the
person whose device and provider account are involved.

This type is the same shape as presenceAudience and mediaAuthorizer, for the same
reason: fanout routes, and it has no business knowing that accounts have settings.
It asks a one-method interface; the gateway, which already holds the user
directory, answers.
*/
type previewPolicy struct {
	users store.UserStore
}

// NewPreviewPolicy builds the gate fanout consults before putting message text in
// a push payload.
//
// Exported so cmd/server can wire it. A deployment that does not wire one sends no
// previews at all — the opposite of how the other optional gates behave, and
// deliberately so: an unwired presence gate keeps the old behaviour because the old
// behaviour was reasonable, while here the old behaviour was the leak.
func NewPreviewPolicy(users store.UserStore) interface {
	WantsPushPreview(ctx context.Context, userID string) bool
} {
	return previewPolicy{users: users}
}

// WantsPushPreview reports whether this account has asked for message text in its
// notifications.
//
// No error in the signature, which is the whole design of this method: the safe
// answer to "the lookup failed" is NO preview, and a returned error is something a
// caller can mishandle into consent. Failing closed is also the right trade on the
// merits — a notification without a preview is less useful, while a preview sent
// against someone's wishes cannot be taken back from a third party's logs.
func (p previewPolicy) WantsPushPreview(ctx context.Context, userID string) bool {
	if p.users == nil {
		return false
	}
	u, err := p.users.GetUser(ctx, userID)
	if err != nil || u == nil {
		return false
	}
	return u.Privacy.PushPreview
}
