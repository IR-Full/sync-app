package keydir

import "time"

// opTimeout bounds one directory round trip when the caller's context carries no
// deadline of its own. A backend can be a remote Redis or another process, so a
// client waiting on a prekey fetch must not be able to wait forever because a
// dependency wedged.
const opTimeout = 3 * time.Second

// MaxOneTimePreKeys caps the one-time prekeys the directory holds per device.
//
// Prekeys are ACCUMULATED across publishes — that is the point, since each is
// consumed by one fetch — so without a ceiling a device can grow its own bucket
// without bound by publishing repeatedly, and the store is remembered per device
// in memory or in Redis with no expiry. The cap is generous next to real client
// behaviour (clients top up a small reserve, ~100), so the only thing it stops is
// the unbounded case. Over the cap the OLDEST are dropped: a fresh key is the one
// worth keeping, and the sender's X3DH works with no one-time prekey at all.
const MaxOneTimePreKeys = 256

// MaxPreKeysPerPublish bounds a single KEY_PUBLISH frame, so one request cannot
// cost an unbounded write even though the total is capped.
const MaxPreKeysPerPublish = 128

// EntryTTL is how long a device's directory entry survives without a republish.
//
// Without one the directory only ever grew: every device that ever connected
// kept a bundle, a prekey list and a set membership forever, including devices
// wiped, reinstalled or logged out years ago. That is both unbounded storage and
// a needless metadata trail — the entry names a device that no longer exists.
//
// The value is generous on purpose. Clients republish whenever they start (they
// have to: one-time prekeys are consumed by fetches and need topping up), so a
// device in real use refreshes this many times over. A device that has not
// published in three months is not one a peer should be opening a session with.
const EntryTTL = 90 * 24 * time.Hour
