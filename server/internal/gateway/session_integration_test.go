package gateway_test

import (
	"net"
	"testing"

	"github.com/IR-Full/sync-app/server/pkg/wire"
)

// sessionsOf asks for the caller's session list and returns it.
func sessionsOf(t *testing.T, c *testClient, reqID uint64) wire.SessionsBody {
	t.Helper()
	c.send(t, wire.MsgSessionList, reqID, wire.SessionListBody{})
	e := c.readUntil(t, wire.MsgSessions)
	var body wire.SessionsBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	return body
}

func TestSessionListShowsEveryLiveSession(t *testing.T) {
	addr := startGateway(t)
	first := connect(t, addr, "sessuser", "secret123")
	second := login(t, addr, "sessuser", "secret123")

	got := sessionsOf(t, first, 1)
	if len(got.Sessions) != 2 {
		t.Fatalf("expected 2 live sessions, got %d (%+v)", len(got.Sessions), got.Sessions)
	}

	// Exactly one entry must be marked current, and it must be this connection's.
	var current int
	for _, s := range got.Sessions {
		if s.Current {
			current++
			if s.SessionID != first.sessionID {
				t.Fatalf("wrong session marked current: %s", s.SessionID)
			}
		}
		if s.DeviceID == "" {
			t.Errorf("session %s has no device id", s.SessionID)
		}
	}
	if current != 1 {
		t.Fatalf("expected exactly one current session, got %d", current)
	}
	_ = second
}

// The list is the one place a session's details are handed to a client, so it is
// the one place a token could leak. The wire type has no field for one; this
// asserts the shape stays that way, because adding it later would be a one-line
// change that turns a read into a lateral-movement tool.
func TestSessionListCarriesNoTokens(t *testing.T) {
	addr := startGateway(t)
	c := connect(t, addr, "sesstok", "secret123")

	raw := func() []byte {
		c.send(t, wire.MsgSessionList, 1, wire.SessionListBody{})
		return c.readUntil(t, wire.MsgSessions).Body
	}()

	for _, secret := range []string{c.token, c.resumeToken} {
		if secret == "" {
			continue
		}
		if containsSub(raw, secret) {
			t.Fatal("a session token appeared in the SESSIONS frame")
		}
	}
}

func containsSub(haystack []byte, needle string) bool {
	n := []byte(needle)
	for i := 0; i+len(n) <= len(haystack); i++ {
		if string(haystack[i:i+len(n)]) == string(n) {
			return true
		}
	}
	return false
}

func TestSessionRevokeKillsOneSession(t *testing.T) {
	addr := startGateway(t)
	keeper := connect(t, addr, "revuser", "secret123")
	doomed := login(t, addr, "revuser", "secret123")

	keeper.send(t, wire.MsgSessionRevoke, 2, wire.SessionRevokeBody{SessionID: doomed.sessionID})
	e := keeper.readUntil(t, wire.MsgSessionRevoked)
	var done wire.SessionRevokedBody
	_ = wire.Unmarshal(e.Body, &done)
	if done.Revoked != 1 || done.Self {
		t.Fatalf("revoke one: %+v", done)
	}

	// The revoked session's token must stop authenticating. This is the property
	// that did not exist before: logging out used to be purely local.
	if authenticates(t, addr, doomed.token) {
		t.Fatal("a revoked session's token still authenticates")
	}
	if !authenticates(t, addr, keeper.token) {
		t.Fatal("revoking one session killed another")
	}
}

// A session id is not a secret — it travels in AUTH_OK and in the session list —
// so an unscoped revoke would let any account sign out any other.
func TestSessionRevokeRefusesSomeoneElsesSession(t *testing.T) {
	addr := startGateway(t)
	victim := connect(t, addr, "revvictim", "secret123")
	attacker := connect(t, addr, "revattacker", "secret123")

	attacker.send(t, wire.MsgSessionRevoke, 2, wire.SessionRevokeBody{SessionID: victim.sessionID})
	e := attacker.readUntil(t, wire.MsgError)
	var eb wire.ErrorBody
	_ = wire.Unmarshal(e.Body, &eb)
	// NOT_FOUND rather than FORBIDDEN, on purpose: "that is not yours" confirms
	// the session exists, which is what the id space must not become.
	if eb.Code != wire.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %d (%q)", eb.Code, eb.Message)
	}
	if !authenticates(t, addr, victim.token) {
		t.Fatal("another account revoked this session")
	}
}

func TestSessionRevokeAllSparesTheCurrentConnection(t *testing.T) {
	addr := startGateway(t)
	me := connect(t, addr, "revall", "secret123")
	other1 := login(t, addr, "revall", "secret123")
	other2 := login(t, addr, "revall", "secret123")

	me.send(t, wire.MsgSessionRevoke, 3, wire.SessionRevokeBody{})
	e := me.readUntil(t, wire.MsgSessionRevoked)
	var done wire.SessionRevokedBody
	_ = wire.Unmarshal(e.Body, &done)
	if done.Revoked != 2 || done.Self {
		t.Fatalf("revoke all-but-current: %+v", done)
	}

	// "Sign out everywhere else" must leave the device you are holding signed in —
	// otherwise securing a lost phone logs you out of the one you used to do it.
	if !authenticates(t, addr, me.token) {
		t.Fatal("revoke-all-but-current killed the current session")
	}
	for i, c := range []*testClient{other1, other2} {
		if authenticates(t, addr, c.token) {
			t.Fatalf("other session %d survived revoke-all", i)
		}
	}
	if left := sessionsOf(t, me, 4); len(left.Sessions) != 1 {
		t.Fatalf("expected 1 surviving session, got %d", len(left.Sessions))
	}
}

func TestSessionRevokeAllIncludingCurrent(t *testing.T) {
	addr := startGateway(t)
	me := connect(t, addr, "revallme", "secret123")
	other := login(t, addr, "revallme", "secret123")

	me.send(t, wire.MsgSessionRevoke, 3, wire.SessionRevokeBody{AllIncludingCurrent: true})
	e := me.readUntil(t, wire.MsgSessionRevoked)
	var done wire.SessionRevokedBody
	_ = wire.Unmarshal(e.Body, &done)
	if done.Revoked != 2 || !done.Self {
		t.Fatalf("revoke all: %+v", done)
	}
	for name, tok := range map[string]string{"current": me.token, "other": other.token} {
		if authenticates(t, addr, tok) {
			t.Fatalf("%s session survived a full revoke", name)
		}
	}
}

// Account deletion re-checks the password even though the socket is already
// authenticated. A session token lives on the device, so without this anyone
// holding an unlocked phone could destroy the account behind it.
func TestAccountDeleteRequiresThePassword(t *testing.T) {
	addr := startGateway(t)
	c := connect(t, addr, "delwrong", "secret123")

	c.send(t, wire.MsgAccountDelete, 1, wire.AccountDeleteBody{Password: "not-the-password"})
	e := c.readUntil(t, wire.MsgError)
	var eb wire.ErrorBody
	_ = wire.Unmarshal(e.Body, &eb)
	if eb.Code != wire.ErrForbidden {
		t.Fatalf("want ErrForbidden, got %d (%q)", eb.Code, eb.Message)
	}
	if !authenticates(t, addr, c.token) {
		t.Fatal("a failed deletion killed the session anyway")
	}
}

func TestAccountDeleteRejectsAnEmptyPassword(t *testing.T) {
	addr := startGateway(t)
	c := connect(t, addr, "delempty", "secret123")

	c.send(t, wire.MsgAccountDelete, 1, wire.AccountDeleteBody{})
	e := c.readUntil(t, wire.MsgError)
	var eb wire.ErrorBody
	_ = wire.Unmarshal(e.Body, &eb)
	// BAD_ARG, not FORBIDDEN: an empty field is a malformed request, and reporting
	// it as a rejected password would send a client to a "wrong password" screen.
	if eb.Code != wire.ErrBadArg {
		t.Fatalf("want ErrBadArg, got %d (%q)", eb.Code, eb.Message)
	}
}

func TestAccountDeleteErasesEverySession(t *testing.T) {
	addr := startGateway(t)
	me := connect(t, addr, "delme", "secret123")
	other := login(t, addr, "delme", "secret123")

	me.send(t, wire.MsgAccountDelete, 1, wire.AccountDeleteBody{Password: "secret123", Reason: "testing"})
	e := me.readUntil(t, wire.MsgAccountDeleted)
	var done wire.AccountDeletedBody
	_ = wire.Unmarshal(e.Body, &done)
	if done.UserID != me.userID || done.DeletedAt == 0 {
		t.Fatalf("account deleted body: %+v", done)
	}

	// Every session of the account, not just the one that asked.
	for name, tok := range map[string]string{"current": me.token, "other": other.token} {
		if authenticates(t, addr, tok) {
			t.Fatalf("%s session survived account deletion", name)
		}
	}

	// And the credentials themselves must stop working: a deleted account that can
	// still log in was never deleted.
	cn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cn.Close() }()
	probe := &testClient{conn: wire.NewConn(wire.NewTCPTransport(cn), false)}
	probe.send(t, wire.MsgHello, 0, wire.HelloBody{ClientVersion: "test", Platform: "cli"})
	probe.read(t)
	probe.send(t, wire.MsgAuth, 1, wire.AuthBody{Username: "delme", Password: "secret123"})
	if e := probe.read(t); e.Type == wire.MsgAuthOK {
		t.Fatal("a deleted account still logs in")
	}
}

// Resolving "@name" is a username-existence oracle. PROFILE_GET was metered for
// exactly that reason while HISTORY reached the same resolution through
// resolveChat for free — so the charge now sits on the resolution itself.
func TestHandleLookupOfAStrangerIsMetered(t *testing.T) {
	addr := startGateway(t)
	c := connect(t, addr, "enumerator", "secret123")

	// The default per-user budget is small, so a probe loop exhausts it quickly.
	// Every miss must eventually answer "slow down" rather than "no such user".
	throttled := false
	for i := 0; i < 60 && !throttled; i++ {
		c.send(t, wire.MsgHistory, uint64(100+i), wire.HistoryBody{ChatID: "@ghost" + itoaTest(i)})
		e := c.readUntil(t, wire.MsgError)
		var eb wire.ErrorBody
		_ = wire.Unmarshal(e.Body, &eb)
		if eb.Code == wire.ErrRateLimited {
			throttled = true
		}
	}
	if !throttled {
		t.Fatal("username enumeration through HISTORY was never throttled")
	}
}

// ...but the budget must not touch ordinary use. A client may address every
// message as "@name", so charging a resolution that lands on an existing chat
// would cap the send rate at the lookup rate.
func TestHandleLookupOfAnExistingChatIsFree(t *testing.T) {
	addr := startGateway(t)
	alice := connect(t, addr, "freealice", "secret123")
	bob := connect(t, addr, "freebob", "secret123")

	// First send opens the chat and pays the discovery budget.
	alice.send(t, wire.MsgSend, 1, wire.SendBody{ChatID: "@freebob", DedupKey: "open", Text: "hi"})
	alice.readUntil(t, wire.MsgSendAck)

	// Everything after it addresses a chat that already exists. More sends than the
	// per-user lookup budget allows (burst 20), and fewer than the per-connection
	// WRITE budget (burst 40) — the point is to prove the lookup charge is absent,
	// not to rediscover the send limit.
	const n = 25
	for i := 0; i < n; i++ {
		alice.send(t, wire.MsgSend, uint64(200+i), wire.SendBody{
			ChatID: "@freebob", DedupKey: "free" + itoaTest(i), Text: "m",
		})
		e := alice.readUntil(t, wire.MsgSendAck)
		var ack wire.SendAckBody
		_ = wire.Unmarshal(e.Body, &ack)
		if ack.MessageID == "" {
			t.Fatalf("send %d was not acked", i)
		}
	}
	_ = bob
}
