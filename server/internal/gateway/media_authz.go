package gateway

import (
	"context"

	"github.com/SyncApp-chat/SyncApp/internal/media"
	"github.com/SyncApp-chat/SyncApp/internal/store"
)

/*
mediaAuthorizer answers "may this user download this blob".

It lives here rather than in internal/media because the answer is assembled from
things the media service has no business knowing: the message log, chat
membership, and the user directory. The media service asks the question through
a one-method interface and stays a blob store with a signing key.

Three ways a fetch is legitimate, checked cheapest-first:

 1. The blob is someone's profile avatar. Profiles are readable by anyone who can
    name the account (PROFILE_GET takes a bare user id), so the picture that goes
    with one carries no additional secret. Checked first because it is a single
    indexed lookup and covers the highest-volume case — a chat list draws dozens
    of avatars per screen.

 2. The blob is reachable from a message in a chat the user belongs to. This is
    the substantive check. A forward carries the original's ref into another
    chat, so the blob can legitimately live in several; membership in any ONE is
    enough, because someone who can read the forward can already see the picture.

 3. Nothing else. A ref that leaked out of a chat the requester is not in stops
    being enough on its own, which is the whole point.

Deliberately NOT a fourth case for "I uploaded this and have not sent it yet":
that would need an owner record the media service does not keep, and the client
already holds the bytes it just uploaded — it has no reason to fetch them back.
*/
type mediaAuthorizer struct {
	svc *Services
}

// fetchGatedMedia is the subset of *media.Service that accepts a download gate.
// An interface rather than the concrete type so the gateway keeps depending on
// behaviour, and so a deployment can substitute a media service that has no
// notion of one.
type fetchGatedMedia interface {
	WithFetchAuthorizer(media.FetchAuthorizer) *media.Service
}

func (m mediaAuthorizer) MayFetch(ctx context.Context, userID, ref string) (bool, error) {
	if userID == "" || ref == "" {
		return false, nil
	}

	// 1. An avatar.
	if m.svc.Users != nil {
		if finder, ok := m.svc.Users.(store.AvatarRefFinder); ok {
			isAvatar, err := finder.AvatarRefExists(ctx, ref)
			if err != nil {
				return false, err
			}
			if isAvatar {
				return true, nil
			}
		}
	}

	// 2. A chat the user is in.
	resolver := m.svc.MediaChats
	if resolver == nil {
		// A backend that cannot answer leaves the decision where it was before this
		// check existed: the signed, unguessable ref. Denying instead would take
		// media away wholesale on a store that simply lacks the capability.
		return true, nil
	}
	chats, err := resolver.MediaRefChats(ctx, ref, mediaAuthChatLimit)
	if err != nil {
		return false, err
	}
	for _, chatID := range chats {
		member, err := m.svc.Chat.IsMember(ctx, chatID, userID)
		if err != nil {
			// A chat that vanished between the two reads is not an error worth
			// failing the whole check for; the remaining candidates still count.
			continue
		}
		if member {
			return true, nil
		}
	}

	return false, nil
}

// mediaAuthChatLimit bounds how many chats are considered for one blob. A widely
// forwarded picture does not need every chat enumerated to answer one membership
// question, and an unbounded read here would be the expensive half of a cheap
// check.
const mediaAuthChatLimit = 32
