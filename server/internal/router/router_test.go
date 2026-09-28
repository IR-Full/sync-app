package router

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
)

/*
 * The router is what lifts the gateway off its single-node ceiling: without it a
 * message only reaches recipients that happened to land on the node processing
 * the event. Every bug here is a delivery that silently does not happen — a
 * binding dropped too early, or a node left in the list after its connection
 * closed, which sends the delivery to a node that no longer holds the socket.
 */

func bg() context.Context { return context.Background() }

func nodesFor(t *testing.T, r Router, userID string) []string {
	t.Helper()
	nodes, err := r.NodesFor(bg(), userID)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(nodes)
	return nodes
}

// ------------------------------------------------------------ DeliverSubject

func TestDeliverSubjectIsPerNode(t *testing.T) {
	// Each gateway subscribes to exactly its own subject; a shared one would
	// hand every node every delivery and each would drop the ones it cannot
	// route — correct, but with fanout multiplied by the size of the fleet.
	if DeliverSubject("node-1") == DeliverSubject("node-2") {
		t.Fatal("two nodes share a delivery subject")
	}
}

func TestDeliverSubjectNamesTheNode(t *testing.T) {
	if got := DeliverSubject("node-1"); got != "deliver.node-1" {
		t.Errorf("subject = %q, want deliver.node-1", got)
	}
}

// ----------------------------------------------------------- NodeDelivery

func TestNodeDeliveryRoundTrips(t *testing.T) {
	original := NodeDelivery{
		Users: []string{"u1"}, DeviceID: "d1", Type: 10, Body: []byte{0x01, 0x02, 0xff},
	}

	got, err := DecodeNodeDelivery(original.Encode())
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Users) != 1 || got.Users[0] != "u1" || got.DeviceID != original.DeviceID {
		t.Errorf("addressing lost: %+v", got)
	}
	if got.Type != original.Type {
		t.Errorf("type = %d, want %d", got.Type, original.Type)
	}
	if string(got.Body) != string(original.Body) {
		t.Errorf("body = %v, want %v", got.Body, original.Body)
	}
}

func TestNodeDeliveryCarriesArbitraryBytes(t *testing.T) {
	/*
	 * The body is an already-encoded protobuf envelope, so it is binary and not
	 * text. It now travels as raw bytes behind an explicit length rather than as
	 * base64 inside JSON, which is where the +33% went — but the property that
	 * has to hold is unchanged: every one of the 256 byte values survives.
	 */
	body := make([]byte, 256)
	for i := range body {
		body[i] = byte(i)
	}

	got, err := DecodeNodeDelivery(NodeDelivery{Users: []string{"u1"}, Type: 8, Body: body}.Encode())
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Body) != len(body) {
		t.Fatalf("body is %d bytes, want %d", len(got.Body), len(body))
	}
	for i := range body {
		if got.Body[i] != body[i] {
			t.Fatalf("byte %d = %#x, want %#x", i, got.Body[i], body[i])
		}
	}
}

func TestNodeDeliveryWithoutADeviceIsUserWide(t *testing.T) {
	// Ordinary delivery goes to every device of a user; only secret chats target
	// one. An empty device id must survive as empty rather than as a literal "".
	got, err := DecodeNodeDelivery(NodeDelivery{Users: []string{"u1"}, Type: 10}.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if got.DeviceID != "" {
		t.Errorf("device = %q, want empty for a user-wide delivery", got.DeviceID)
	}
}

func TestNodeDeliveryWithNoBodyRoundTrips(t *testing.T) {
	// Bodiless types (PING, T_ACK) travel through the same envelope.
	got, err := DecodeNodeDelivery(NodeDelivery{Users: []string{"u1"}, Type: 6}.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Body) != 0 {
		t.Errorf("body = %v, want none", got.Body)
	}
}

func TestDecodeNodeDeliveryRejectsGarbage(t *testing.T) {
	// It runs on bytes off a shared bus; a malformed payload has to be an error
	// the consumer can drop, not a half-populated delivery it acts on.
	for name, payload := range map[string][]byte{
		"empty":          {},
		"unknown marker": {0x7F, 0x00, 0x01},
		"truncated":      {nodeDeliveryV1, 0x00},
		"bad json":       []byte("{not json"),
		"lying user count": {
			nodeDeliveryV1, 0x00, 0x0A, // marker, type
			0x00, 0x00, // no device
			0xFF, 0xFF, // 65535 recipients that are not there
		},
		"body longer than the frame": {
			nodeDeliveryV1, 0x00, 0x0A,
			0x00, 0x00, // no device
			0x00, 0x00, // no users
			0xFF, 0xFF, 0xFF, 0xFF, // 4 GiB of body in a 11-byte frame
		},
	} {
		if _, err := DecodeNodeDelivery(payload); err == nil {
			t.Errorf("%s decoded as a delivery", name)
		}
	}
}

// TestNodeDeliveryCarriesManyRecipients is the point of the current shape: one
// delivery per NODE naming everyone on it, instead of one per recipient each
// carrying its own copy of the body.
func TestNodeDeliveryCarriesManyRecipients(t *testing.T) {
	users := make([]string, 300)
	for i := range users {
		users[i] = fmt.Sprintf("user-%d", i)
	}
	body := []byte("a four kilobyte message would be copied 300 times before")

	encoded := NodeDelivery{Users: users, Type: 10, Body: body}.Encode()
	got, err := DecodeNodeDelivery(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Users) != len(users) {
		t.Fatalf("got %d recipients, want %d", len(got.Users), len(users))
	}
	for i := range users {
		if got.Users[i] != users[i] {
			t.Fatalf("recipient %d = %q, want %q", i, got.Users[i], users[i])
		}
	}
	// The body appears ONCE. Per-recipient publishing would have put 300 copies
	// of it on the bus; this asserts the encoding cannot silently regress to that.
	if bytes.Count(encoded, body) != 1 {
		t.Fatalf("body appears %d times in one delivery", bytes.Count(encoded, body))
	}
}

// TestDecodeAcceptsTheLegacyJSONForm keeps a rolling deploy from being an
// ordering problem: while both builds are publishing, either node must be able to
// read either frame.
func TestDecodeAcceptsTheLegacyJSONForm(t *testing.T) {
	legacy := []byte(`{"u":"u7","d":"dev-2","t":54,"b":"AQL/"}`)

	got, err := DecodeNodeDelivery(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Users) != 1 || got.Users[0] != "u7" {
		t.Fatalf("recipients = %v, want [u7]", got.Users)
	}
	if got.DeviceID != "dev-2" || got.Type != 54 {
		t.Fatalf("addressing lost: %+v", got)
	}
	if string(got.Body) != string([]byte{0x01, 0x02, 0xff}) {
		t.Fatalf("body = %v, want [1 2 255]", got.Body)
	}
}

// TestBinaryEncodingBeatsJSONOnSize is the regression guard for WHY this codec
// exists. It is not a benchmark — it is the claim that the format does not quietly
// go back to carrying a base64 payload.
func TestBinaryEncodingBeatsJSONOnSize(t *testing.T) {
	body := make([]byte, 4096)
	nd := NodeDelivery{Users: []string{"1234567890123456789"}, Type: 10, Body: body}

	binarySize := len(nd.Encode())
	// What JSON cost: base64 inflates the body by 4/3 before any framing.
	jsonBodySize := (len(body) + 2) / 3 * 4
	if binarySize >= jsonBodySize {
		t.Fatalf("encoding is %d bytes for a %d-byte body; JSON's base64 alone was %d",
			binarySize, len(body), jsonBodySize)
	}
	if overhead := binarySize - len(body); overhead > 64 {
		t.Fatalf("framing overhead is %d bytes; it should be a few fixed fields", overhead)
	}
}

// ------------------------------------------------------------ memory router

func TestBindMakesAUserReachable(t *testing.T) {
	r := NewMemory()

	if err := r.Bind(bg(), "u1", "d1", "node-1"); err != nil {
		t.Fatal(err)
	}

	if got := nodesFor(t, r, "u1"); len(got) != 1 || got[0] != "node-1" {
		t.Errorf("nodes = %v, want [node-1]", got)
	}
}

func TestAnUnknownUserIsOnNoNodes(t *testing.T) {
	// Offline is the common case — fanout uses an empty list to decide a push is
	// needed, so this must be an empty slice and not an error.
	r := NewMemory()

	got, err := r.NodesFor(bg(), "never-connected")
	if err != nil {
		t.Fatalf("an unknown user produced an error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("nodes = %v, want none", got)
	}
}

func TestAUserOnSeveralNodesIsReachableOnAllOfThem(t *testing.T) {
	// A phone on one node and a laptop on another is the ordinary multi-device
	// case; missing one would deliver to only half the user's devices.
	r := NewMemory()

	for node := range map[string]bool{"node-1": true, "node-2": true} {
		if err := r.Bind(bg(), "u1", "d-"+node, node); err != nil {
			t.Fatal(err)
		}
	}

	if got := nodesFor(t, r, "u1"); len(got) != 2 {
		t.Errorf("nodes = %v, want both", got)
	}
}

func TestANodeIsListedOnceHoweverManyDevicesItHolds(t *testing.T) {
	// Fanout publishes one delivery per node; a duplicated node would make the
	// receiving gateway write the same frame twice to every local connection.
	r := NewMemory()

	for _, device := range []string{"d1", "d2", "d3"} {
		if err := r.Bind(bg(), "u1", device, "node-1"); err != nil {
			t.Fatal(err)
		}
	}

	if got := nodesFor(t, r, "u1"); len(got) != 1 {
		t.Errorf("nodes = %v, want node-1 listed once", got)
	}
}

/*
 * The refcount is the whole point of the memory router. A user with three tabs
 * on one node closes one: dropping the node on that first Unbind would strand
 * the other two — they stay connected and stop receiving anything, with no
 * error anywhere and no recovery until they reconnect.
 */
func TestClosingOneDeviceKeepsTheOthersReachable(t *testing.T) {
	r := NewMemory()
	for _, device := range []string{"d1", "d2", "d3"} {
		if err := r.Bind(bg(), "u1", device, "node-1"); err != nil {
			t.Fatal(err)
		}
	}

	if err := r.Unbind(bg(), "u1", "d1", "node-1"); err != nil {
		t.Fatal(err)
	}

	if got := nodesFor(t, r, "u1"); len(got) != 1 {
		t.Errorf("nodes = %v — the remaining devices lost their delivery path", got)
	}
}

func TestTheNodeIsDroppedOnceItsLastDeviceLeaves(t *testing.T) {
	// A node left in the list after its last socket closed sends every later
	// delivery to a gateway that can only discard it — the message is lost and
	// no push is queued, because the user still looks online.
	r := NewMemory()
	for _, device := range []string{"d1", "d2"} {
		if err := r.Bind(bg(), "u1", device, "node-1"); err != nil {
			t.Fatal(err)
		}
	}

	for _, device := range []string{"d1", "d2"} {
		if err := r.Unbind(bg(), "u1", device, "node-1"); err != nil {
			t.Fatal(err)
		}
	}

	if got := nodesFor(t, r, "u1"); len(got) != 0 {
		t.Errorf("nodes = %v, want none once every device has gone", got)
	}
}

func TestUnbindingOneNodeLeavesTheOther(t *testing.T) {
	r := NewMemory()
	if err := r.Bind(bg(), "u1", "d1", "node-1"); err != nil {
		t.Fatal(err)
	}
	if err := r.Bind(bg(), "u1", "d2", "node-2"); err != nil {
		t.Fatal(err)
	}

	if err := r.Unbind(bg(), "u1", "d1", "node-1"); err != nil {
		t.Fatal(err)
	}

	if got := nodesFor(t, r, "u1"); len(got) != 1 || got[0] != "node-2" {
		t.Errorf("nodes = %v, want [node-2]", got)
	}
}

func TestUnbindingAUserWhoWasNeverBoundIsHarmless(t *testing.T) {
	// A close can arrive for a connection whose bind failed, or twice after a
	// reconnect race.
	r := NewMemory()

	if err := r.Unbind(bg(), "never-bound", "d1", "node-1"); err != nil {
		t.Errorf("unbinding an unknown user errored: %v", err)
	}
}

func TestUnbindingTwiceDoesNotDriveTheCountNegative(t *testing.T) {
	/*
	 * A negative refcount would never reach zero again, so the node would stay in
	 * the list forever — the "delivered to a gateway that no longer holds the
	 * socket" failure, made permanent.
	 */
	r := NewMemory()
	if err := r.Bind(bg(), "u1", "d1", "node-1"); err != nil {
		t.Fatal(err)
	}

	for range 3 {
		if err := r.Unbind(bg(), "u1", "d1", "node-1"); err != nil {
			t.Fatal(err)
		}
	}
	// Re-binding once must make the user reachable again, which only holds if the
	// count actually sits at zero rather than below it.
	if err := r.Bind(bg(), "u1", "d1", "node-1"); err != nil {
		t.Fatal(err)
	}

	if got := nodesFor(t, r, "u1"); len(got) != 1 {
		t.Errorf("nodes = %v after rebinding; the refcount went negative", got)
	}
}

func TestUnbindingFromANodeTheUserIsNotOnIsHarmless(t *testing.T) {
	r := NewMemory()
	if err := r.Bind(bg(), "u1", "d1", "node-1"); err != nil {
		t.Fatal(err)
	}

	if err := r.Unbind(bg(), "u1", "d1", "node-2"); err != nil {
		t.Fatal(err)
	}

	if got := nodesFor(t, r, "u1"); len(got) != 1 || got[0] != "node-1" {
		t.Errorf("nodes = %v, want the real binding untouched", got)
	}
}

func TestUsersAreIndependent(t *testing.T) {
	r := NewMemory()
	if err := r.Bind(bg(), "u1", "d1", "node-1"); err != nil {
		t.Fatal(err)
	}
	if err := r.Bind(bg(), "u2", "d1", "node-2"); err != nil {
		t.Fatal(err)
	}

	if got := nodesFor(t, r, "u1"); len(got) != 1 || got[0] != "node-1" {
		t.Errorf("u1 nodes = %v", got)
	}
	if got := nodesFor(t, r, "u2"); len(got) != 1 || got[0] != "node-2" {
		t.Errorf("u2 nodes = %v", got)
	}
}

func TestRefreshIsANoOpForTheMemoryRouter(t *testing.T) {
	// Nothing expires in memory; the heartbeat exists for the Redis backend, and
	// here it must neither error nor disturb the bindings.
	r := NewMemory()
	if err := r.Bind(bg(), "u1", "d1", "node-1"); err != nil {
		t.Fatal(err)
	}

	if err := r.Refresh(bg(), "u1", "node-1"); err != nil {
		t.Errorf("refresh errored: %v", err)
	}

	if got := nodesFor(t, r, "u1"); len(got) != 1 {
		t.Errorf("nodes = %v after a refresh", got)
	}
}

func TestTheMemoryRouterIsSafeForConcurrentUse(t *testing.T) {
	// One gateway process binds and unbinds on every connection open and close,
	// from as many goroutines as it has connections.
	r := NewMemory()
	var wg sync.WaitGroup

	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			device := string(rune('a' + i%26))
			_ = r.Bind(bg(), "u1", device, "node-1")
			_, _ = r.NodesFor(bg(), "u1")
			_ = r.Refresh(bg(), "u1", "node-1")
			_ = r.Unbind(bg(), "u1", device, "node-1")
		}(i)
	}
	wg.Wait()

	// Every bind was matched by an unbind, so nothing may be left behind.
	if got := nodesFor(t, r, "u1"); len(got) != 0 {
		t.Errorf("nodes = %v after every device unbound", got)
	}
}

// TestNodesForManyMatchesNodesForPerUser is the correctness half of batching.
//
// The batch lookup exists to be cheaper, and the only way that is a win rather
// than a bug is if it answers identically. A batch that silently disagreed with
// the per-user path would send duplicate pushes to people who are online, or
// none to people who are not.
func TestNodesForManyMatchesNodesForPerUser(t *testing.T) {
	r := NewMemory()
	if err := r.Bind(bg(), "u1", "d1", "node-1"); err != nil {
		t.Fatal(err)
	}
	if err := r.Bind(bg(), "u1", "d2", "node-2"); err != nil {
		t.Fatal(err)
	}
	if err := r.Bind(bg(), "u2", "d1", "node-1"); err != nil {
		t.Fatal(err)
	}

	users := []string{"u1", "u2", "u3-offline"}
	batch, err := r.NodesForMany(bg(), users)
	if err != nil {
		t.Fatal(err)
	}
	for _, uid := range users {
		want := nodesFor(t, r, uid)
		got := append([]string(nil), batch[uid]...)
		sort.Strings(want)
		sort.Strings(got)
		if len(want) != len(got) {
			t.Fatalf("%s: batch says %v, per-user says %v", uid, got, want)
		}
		for i := range want {
			if want[i] != got[i] {
				t.Fatalf("%s: batch says %v, per-user says %v", uid, got, want)
			}
		}
	}
	// An offline user is ABSENT rather than present-with-empty. Callers treat a
	// missing entry as "needs a push", so the two must not be different states.
	if _, present := batch["u3-offline"]; present {
		t.Error("an offline user has an entry; absence is how offline is reported")
	}
}

// TestNodesForManyOnAnEmptyRequestDoesNothing guards the degenerate case: a chat
// page can come back empty, and that must not become a round trip or a nil map
// the caller indexes into.
func TestNodesForManyOnAnEmptyRequestDoesNothing(t *testing.T) {
	got, err := NewMemory().NodesForMany(bg(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Error("returned a nil map; callers index it without checking")
	}
	if len(got) != 0 {
		t.Errorf("got %d entries for no users", len(got))
	}
}

// TestDeviceBindingIsSeparateFromUserBinding is the routing fix behind the
// secret-chat queue: "is this user online" and "is this device reachable" are
// different questions, and Bind used to discard the device entirely.
func TestDeviceBindingIsSeparateFromUserBinding(t *testing.T) {
	r := NewMemory()
	if err := r.Bind(bg(), "u1", "phone", "node-1"); err != nil {
		t.Fatal(err)
	}

	// The user is online — on the phone.
	if got := nodesFor(t, r, "u1"); len(got) != 1 {
		t.Fatalf("user nodes = %v, want one", got)
	}
	// The laptop is not, and that must be visible. Reporting the user's node here
	// is what made the relay drop ciphertext addressed to an offline device.
	laptop, err := r.NodesForDevice(bg(), "u1", "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if len(laptop) != 0 {
		t.Fatalf("an unbound device resolves to %v; it must resolve to nothing", laptop)
	}
	phone, err := r.NodesForDevice(bg(), "u1", "phone")
	if err != nil {
		t.Fatal(err)
	}
	if len(phone) != 1 || phone[0] != "node-1" {
		t.Fatalf("bound device resolves to %v, want [node-1]", phone)
	}

	// Unbinding the phone must clear both indexes, or a disconnected device stays
	// reachable and its ciphertext is published into the void.
	if err := r.Unbind(bg(), "u1", "phone", "node-1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.NodesForDevice(bg(), "u1", "phone"); len(got) != 0 {
		t.Errorf("device still reachable after unbind: %v", got)
	}
	if got := nodesFor(t, r, "u1"); len(got) != 0 {
		t.Errorf("user still reachable after their only device unbound: %v", got)
	}
}
