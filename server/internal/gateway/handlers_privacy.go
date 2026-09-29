// Handlers for per-user privacy settings, and the checks that enforce them.
//
// The settings themselves are two trivial handlers. What matters is the other
// half of this file: a setting nothing consults is a preference screen, not a
// privacy feature, so the enforcement points live here beside the definitions
// rather than scattered across the handlers that happen to need them.
package gateway

import (
	"context"

	"github.com/IR-Full/sync-app/server/internal/model"
	"github.com/IR-Full/sync-app/server/pkg/wire"
)

func (c *conn) handlePrivacyGet(ctx context.Context, e wire.Envelope) error {
	u, err := c.gw.svc.Users.GetUser(ctx, c.userID)
	if err != nil {
		return c.replyForError(e.RequestID, err)
	}
	return c.reply(wire.MsgPrivacy, e.RequestID, privacyToWire(u.Privacy))
}

// handlePrivacySet replaces the caller's settings.
//
// Every field is required. A partial update would make "nobody" —  the setting
// whose entire purpose is to withhold something — indistinguishable from "not
// specified", and guessing wrong in that direction exposes what the user asked
// to hide.
func (c *conn) handlePrivacySet(ctx context.Context, e wire.Envelope) error {
	var body wire.PrivacySetBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad privacy body")
	}
	p := model.Privacy{
		LastSeen:    model.Visibility(body.LastSeen),
		Avatar:      model.Visibility(body.Avatar),
		Groups:      model.Visibility(body.Groups),
		PushPreview: body.PushPreview,
	}
	// Validated rather than normalized: on the WRITE path a value we do not
	// recognise is a client bug, and silently storing "everyone" instead would be
	// the one failure mode that quietly widens visibility.
	for _, v := range []model.Visibility{p.LastSeen, p.Avatar, p.Groups} {
		if !model.ValidVisibility(v) {
			return c.replyError(e.RequestID, wire.ErrBadArg,
				"privacy must be one of: everyone, contacts, nobody")
		}
	}

	if err := c.gw.svc.Users.UpdatePrivacy(ctx, c.userID, p); err != nil {
		return c.replyForError(e.RequestID, err)
	}
	c.gw.audit(ctx, "user.privacy", c.userID, "", string(p.LastSeen)+"/"+string(p.Avatar)+"/"+string(p.Groups))

	// Mirror to this account's other devices, as PROFILE_SET does: a setting
	// changed on the phone must not read as unchanged on the desktop.
	out := privacyToWire(p)
	c.gw.routeToUser(ctx, c.userID, "", wire.MsgPrivacy, wire.Marshal(out))
	return c.reply(wire.MsgPrivacy, e.RequestID, out)
}

func privacyToWire(p model.Privacy) wire.PrivacyBody {
	p = p.Normalize()
	return wire.PrivacyBody{
		LastSeen:    string(p.LastSeen),
		Avatar:      string(p.Avatar),
		Groups:      string(p.Groups),
		PushPreview: p.PushPreview,
	}
}

// --- Enforcement ---

// maySee reports whether viewer may see an attribute of owner gated by v.
//
// The direction of "contacts" is the part worth being explicit about: it is the
// OWNER's address book that decides. Someone who added me to their contacts has
// not thereby granted themselves a view of my last-seen — that reading would let
// anyone opt themselves in, which is the opposite of a privacy setting.
//
// A contacts lookup that fails is treated as "not a contact". Failing open would
// mean a database blip exposes exactly what the user asked to hide, and the cost
// of failing closed is a temporarily missing avatar.
func (g *Gateway) maySee(ctx context.Context, ownerID, viewerID string, v model.Visibility) bool {
	if ownerID == viewerID {
		return true // your own attributes are always visible to you
	}
	switch model.NormalizeVisibility(v) {
	case model.VisibilityEveryone:
		return true
	case model.VisibilityNobody:
		return false
	default:
		if g.svc.Contacts == nil {
			// No address book in this deployment means "contacts" cannot be
			// evaluated. Denying is the honest reading: the user asked for a
			// narrower audience than everyone, and we cannot identify it.
			return false
		}
		ok, err := g.svc.Contacts.IsContact(ctx, ownerID, viewerID)
		return err == nil && ok
	}
}
