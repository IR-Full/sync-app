package gateway

// Chat-list page bounds. They mirror the ones the chat service enforces: the
// gateway clamps BEFORE calling so it knows the effective page size, which is
// what tells a short page (the last one) from a full one. The service keeps its
// own bound as a floor for any other caller.
const (
	defaultChatListLimit = 100
	maxChatListLimit     = 200
)

// Profile bounds. The display name is a label, not content: it is rendered in
// lists where a long one crowds out everything else, so it is capped in BYTES
// (like message text) rather than runes. The avatar cap is generous enough for
// any media_ref the media service mints and short enough that the field cannot
// be used as free storage.
const (
	maxDisplayName = 64
	maxAvatarRef   = 128
)

// maxMuteUntil bounds a mute deadline.
//
// 2100-01-01. The value doubles as the "forever" the migration writes when it
// carries the old boolean `muted` column forward, so it has to be a date a client
// can render without apology — and a bound at all, because an unbounded deadline
// is harmless to store and confusing to display.
const maxMuteUntil int64 = 4102444800000
