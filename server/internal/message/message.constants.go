package message

import "errors"

// ErrForbidden means the sender may not post/act in the chat.
var ErrForbidden = errors.New("message: forbidden")
