package breaker

import "errors"

// ErrOpen is returned by Do when the breaker is open (dependency assumed down).
var ErrOpen = errors.New("breaker: open")
