package media

import "time"

const (
	// gcMinAge is how long an object must exist before the sweep will consider it.
	// It covers the gap between "bytes uploaded" and "message sent" — an upload in
	// flight is unreferenced by definition, and collecting it would delete the
	// user's file out from under them mid-send. One ticket lifetime is exactly the
	// window in which that can happen.
	gcMinAge = 15 * time.Minute
	// gcEvery is how often the sweep runs. It bounds how long an expired
	// message's bytes can survive its text.
	gcEvery = 5 * time.Minute
)
