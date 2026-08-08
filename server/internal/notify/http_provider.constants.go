package notify

import "fmt"

// errTokenDead marks a token the provider says will never receive again.
var errTokenDead = fmt.Errorf("notify: token unregistered")
