package reaction

import "errors"

// ErrForbidden means the user is not a member of the chat.
var ErrForbidden = errors.New("reaction: forbidden")

// ErrBadEmoji means the emoji failed validation.
var ErrBadEmoji = errors.New("reaction: invalid emoji")

// maxEmojiRunes bounds the reaction token. Emoji are multi-rune (ZWJ sequences,
// skin-tone modifiers), so a few runes are legitimate — but this must never be a
// free-text field, or reactions become an unmoderated message channel.
const maxEmojiRunes = 8
