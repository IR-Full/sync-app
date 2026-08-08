package gateway

// authIdentity is the gateway's flattened view of a resolved principal.
type authIdentity struct {
	userID      string
	deviceID    string
	sessionID   string
	token       string
	resumeToken string
	// The account behind the session, echoed in AUTH_OK. A client that logs in
	// with a stored token never sent a username and has no other way to learn
	// its own.
	username    string
	displayName string
	avatarRef   string
}
