package gateway

import "errors"

// errNewChatThrottled signals the new-conversation rate limit was hit.
var errNewChatThrottled = errors.New("too many new chats")

// errBlocked signals a blocked relationship in either direction.
var errBlocked = errors.New("blocked")
