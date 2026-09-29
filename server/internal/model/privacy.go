package model

/*
Who may see what.

Three settings, deliberately not one. They are read on different paths and
answer different questions: presence fanout asks about last-seen, a profile read
asks about the avatar, and joining a group asks whether the actor may be added at
all. Collapsing them into a single "privacy level" would force the answer to one
of them to be a guess about the others.

The default is Everyone across the board, because that is what the system did
before these existed. Tightening an existing account's visibility in a migration
is a change nobody asked for that reads, from the inside, as the app breaking.
*/

// Visibility is who an attribute is exposed to.
type Visibility string

const (
	// VisibilityEveryone exposes the attribute to any account that can address
	// this user at all — which is still bounded by blocking.
	VisibilityEveryone Visibility = "everyone"
	// VisibilityContacts exposes it only to accounts in this user's address book.
	//
	// The direction matters and is easy to get backwards: it is MY contact list
	// that decides, not theirs. Someone who added me to their contacts has not
	// thereby granted themselves a view of my last-seen.
	VisibilityContacts Visibility = "contacts"
	// VisibilityNobody exposes it to no one.
	VisibilityNobody Visibility = "nobody"
)

// Privacy is a user's visibility settings.
type Privacy struct {
	// LastSeen gates presence: both the online/offline transitions fanned out to
	// direct peers and the last-seen timestamp attached to them.
	LastSeen Visibility `json:"last_seen"`
	// Avatar gates the avatar_ref on a profile read. The display name and handle
	// are NOT gated: they are how an account is addressed and recognised, and
	// hiding them would make a conversation with a blank row rather than a
	// private one.
	Avatar Visibility `json:"avatar"`
	// Groups gates being added to a group by someone else. "nobody" means invite
	// links only — the user can still join, they just cannot be dragged in.
	Groups Visibility `json:"groups"`
	// PushPreview decides whether message TEXT may be sent to the push provider.
	//
	// It is a boolean rather than a Visibility because the audience is not another
	// user: it is Apple and Google. A preview in the FCM/APNs payload hands a third
	// party the message text — a larger disclosure than anything end-to-end
	// encryption protects against, since E2E guards against the server and this is
	// the server volunteering the text.
	//
	// It defaults to FALSE. The other three default to "everyone", because
	// tightening an existing account's visibility reads as the app breaking; this
	// one defaults closed because sending text to a third party is a leak unless
	// someone chose it.
	PushPreview bool `json:"push_preview"`
}

// DefaultPrivacy is what every account starts with, and what a row written
// before these columns existed reads as.
func DefaultPrivacy() Privacy {
	return Privacy{
		LastSeen: VisibilityEveryone,
		Avatar:   VisibilityEveryone,
		Groups:   VisibilityEveryone,
	}
}

// ValidVisibility reports whether v is one of the three settings.
//
// Exported because the boundary check belongs at the gateway, where a bad value
// is a client error to report, rather than at the store, where it would be an
// internal error nobody can act on.
func ValidVisibility(v Visibility) bool {
	switch v {
	case VisibilityEveryone, VisibilityContacts, VisibilityNobody:
		return true
	default:
		return false
	}
}

// NormalizeVisibility maps an empty or unrecognised value to Everyone.
//
// Used on the READ path only. A stored value that is not one of the three means
// the row predates this feature or the database was written by something else;
// either way the safe reading is the historical behaviour, not a refusal that
// would make the account unusable.
func NormalizeVisibility(v Visibility) Visibility {
	if ValidVisibility(v) {
		return v
	}
	return VisibilityEveryone
}

// Normalize returns p with every unrecognised field defaulted.
func (p Privacy) Normalize() Privacy {
	return Privacy{
		LastSeen: NormalizeVisibility(p.LastSeen),
		Avatar:   NormalizeVisibility(p.Avatar),
		Groups:   NormalizeVisibility(p.Groups),
	}
}
