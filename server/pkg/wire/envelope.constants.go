package wire

import "errors"

// ErrShortEnvelope is returned when the byte slice is truncated.
var ErrShortEnvelope = errors.New("wire: short envelope")
