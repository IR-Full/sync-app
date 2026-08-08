package breaker

const (
	Closed   State = iota // healthy: calls allowed
	Open                  // failing: calls rejected until cooldown
	HalfOpen              // probing: limited calls allowed to test recovery
)
