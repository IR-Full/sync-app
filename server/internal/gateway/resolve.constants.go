package gateway

import "errors"

// errNewChatThrottled signals the new-conversation rate limit was hit.
var errNewChatThrottled = errors.New("too many new chats")

// errBlocked signals a blocked relationship in either direction.
var errBlocked = errors.New("blocked")

// errLookupThrottled signals the per-user handle-lookup budget was hit.
//
// Resolving "@name" is a username-existence oracle wherever it happens, and it
// happens in more places than the one that was metered for it: PROFILE_GET was
// added to stateChanging precisely because it resolves handles, while HISTORY
// resolved them through resolveChat for free. Charging the resolution itself,
// rather than the messages that happen to reach it, is the only version of this
// that does not need re-auditing every time a handler gains a chat target.
var errLookupThrottled = errors.New("too many handle lookups")
