package fanout

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/SyncApp-chat/SyncApp/internal/router"
	"github.com/SyncApp-chat/SyncApp/pkg/eventbus"
	"github.com/SyncApp-chat/SyncApp/pkg/wire"
)

/*
 * Fanout decides WHO receives each event, and that is the whole security surface
 * of the delivery path: a broadcast that reaches one person too many is a
 * disclosure, and one that reaches one too few is a message that silently never
 * arrives. Neither shows up as an error anywhere.
 */

// deliveryRouter records every USER the service tried to reach and can report a
// user as online so the push-suppression paths are reachable.
//
// User, not (user, device): the router lookup is keyed by user — the device id
// rides inside the NodeDelivery envelope the owning node receives, which is
// where TestRouteSecretTargetsOneDevice asserts on it.
type deliveryRouter struct {
	mu       sync.Mutex
	delivers []delivery
	online   map[string]bool
}

type delivery struct {
	user string
}

func newDeliveryRouter(online ...string) *deliveryRouter {
	set := map[string]bool{}
	for _, u := range online {
		set[u] = true
	}
	return &deliveryRouter{online: set}
}

func (r *deliveryRouter) NodesFor(_ context.Context, userID string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.delivers = append(r.delivers, delivery{user: userID})
	if r.online[userID] {
		return []string{"node-1"}, nil
	}
	return nil, nil
}

func (r *deliveryRouter) NodesForMany(_ context.Context, userIDs []string) (map[string][]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string][]string, len(userIDs))
	for _, uid := range userIDs {
		r.delivers = append(r.delivers, delivery{user: uid})
		if r.online[uid] {
			out[uid] = []string{"node-1"}
		}
	}
	return out, nil
}
func (r *deliveryRouter) NodesForDevice(ctx context.Context, userID, _ string) ([]string, error) {
	return r.NodesFor(ctx, userID)
}

func (r *deliveryRouter) Bind(context.Context, string, string, string) error   { return nil }
func (r *deliveryRouter) Unbind(context.Context, string, string, string) error { return nil }
func (r *deliveryRouter) Refresh(context.Context, string, string) error        { return nil }

// reached lists the users the service attempted to deliver to, deduplicated.
func (r *deliveryRouter) reached() map[string]bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]bool{}
	for _, d := range r.delivers {
		out[d.user] = true
	}
	return out
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newFanout(members []string, online ...string) (*Service, *captureBus, *deliveryRouter) {
	bus := &captureBus{}
	rtr := newDeliveryRouter(online...)
	return New(bus, staticChats{ids: members}, rtr, quiet()), bus, rtr
}

func event(subject string, body any) eventbus.Event {
	return eventbus.Event{Subject: subject, Key: "c1", Data: wire.Marshal(body)}
}

// ---------------------------------------------------------------- reactions

func TestReactionReachesEveryMember(t *testing.T) {
	svc, _, rtr := newFanout([]string{"u1", "u2", "u3"})

	if err := svc.onReaction(context.Background(), event(eventbus.SubjReaction,
		wire.ReactUpdateBody{ChatID: "c1", MessageID: "m1", UserID: "u1", Emoji: "👍", Added: true},
	)); err != nil {
		t.Fatal(err)
	}

	reached := rtr.reached()
	for _, want := range []string{"u1", "u2", "u3"} {
		if !reached[want] {
			t.Errorf("%s did not receive the reaction", want)
		}
	}
}

/*
 * The reactor's OWN other devices do receive it — that is what keeps a phone and
 * a laptop showing the same tally. Reactions are the one broadcast where the
 * originator is deliberately included, unlike typing and read receipts.
 */
func TestReactionIsEchoedToTheReactorsOwnDevices(t *testing.T) {
	svc, _, rtr := newFanout([]string{"u1", "u2"})

	if err := svc.onReaction(context.Background(), event(eventbus.SubjReaction,
		wire.ReactUpdateBody{ChatID: "c1", MessageID: "m1", UserID: "u1", Emoji: "👍", Added: true},
	)); err != nil {
		t.Fatal(err)
	}

	if !rtr.reached()["u1"] {
		t.Error("the reactor's own devices were skipped; multi-device would drift")
	}
}

func TestReactionSendsNoPush(t *testing.T) {
	// A reaction is not worth waking a phone for. Offline devices pick it up
	// with the message on next sync.
	svc, bus, _ := newFanout([]string{"u1", "u2"})

	if err := svc.onReaction(context.Background(), event(eventbus.SubjReaction,
		wire.ReactUpdateBody{ChatID: "c1", MessageID: "m1", UserID: "u1", Emoji: "👍"},
	)); err != nil {
		t.Fatal(err)
	}

	if got := bus.bySubject(eventbus.SubjNotifyPush); len(got) != 0 {
		t.Errorf("a reaction queued %d push jobs", len(got))
	}
}

func TestReactionRejectsAMalformedBody(t *testing.T) {
	svc, _, _ := newFanout([]string{"u1"})

	err := svc.onReaction(context.Background(), eventbus.Event{
		Subject: eventbus.SubjReaction, Data: []byte("not a valid body"),
	})

	if err == nil {
		t.Error("a malformed event was accepted")
	}
}

// ------------------------------------------------------------------- typing

/*
 * Typing is the highest-frequency signal in the system, so the exclusion is not
 * politeness — echoing it back would double the traffic it generates and show
 * every user their own indicator.
 */
func TestTypingSkipsTheTypist(t *testing.T) {
	svc, _, rtr := newFanout([]string{"u1", "u2", "u3"})

	if err := svc.onTyping(context.Background(), event(eventbus.SubjTyping,
		wire.TypingBody{ChatID: "c1", UserID: "u1", Active: true},
	)); err != nil {
		t.Fatal(err)
	}

	reached := rtr.reached()
	if reached["u1"] {
		t.Error("the typist received their own typing indicator")
	}
	for _, want := range []string{"u2", "u3"} {
		if !reached[want] {
			t.Errorf("%s did not receive the typing indicator", want)
		}
	}
}

func TestTypingSendsNoPush(t *testing.T) {
	// It expires in seconds; a notification for it would be gone before the
	// phone finished vibrating.
	svc, bus, _ := newFanout([]string{"u1", "u2"})

	if err := svc.onTyping(context.Background(), event(eventbus.SubjTyping,
		wire.TypingBody{ChatID: "c1", UserID: "u1", Active: true},
	)); err != nil {
		t.Fatal(err)
	}

	if got := bus.bySubject(eventbus.SubjNotifyPush); len(got) != 0 {
		t.Errorf("typing queued %d push jobs", len(got))
	}
}

func TestTypingRejectsAMalformedBody(t *testing.T) {
	svc, _, _ := newFanout([]string{"u1"})

	if err := svc.onTyping(context.Background(), eventbus.Event{
		Subject: eventbus.SubjTyping, Data: []byte("not a valid body"),
	}); err == nil {
		t.Error("a malformed event was accepted")
	}
}

// -------------------------------------------------------------------- polls

func TestPollStateReachesEveryMember(t *testing.T) {
	svc, _, rtr := newFanout([]string{"u1", "u2", "u3"})

	if err := svc.onPollState(context.Background(), event(eventbus.SubjPollState,
		wire.PollStateBody{PollID: "p1", ChatID: "c1", Question: "Lunch?"},
	)); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"u1", "u2", "u3"} {
		if !rtr.reached()[want] {
			t.Errorf("%s did not receive the poll tally", want)
		}
	}
}

/*
 * This is the disclosure the handler defends against. `MyVotes` is filled in
 * per-recipient on the reply path; a broadcast that carried one member's
 * selections would tell the whole chat how that person voted — in an anonymous
 * poll, which is precisely what anonymity was promised against.
 */
func TestPollStateStripsThePersonalVoteBeforeBroadcasting(t *testing.T) {
	svc, _, _ := newFanout([]string{"u1", "u2"})
	var captured wire.PollStateBody

	// Re-decode what was actually routed by intercepting the payload the
	// handler built.
	svc.router = &payloadCapturingRouter{onPayload: func(data []byte) {
		_ = wire.Unmarshal(data, &captured)
	}}

	if err := svc.onPollState(context.Background(), event(eventbus.SubjPollState,
		wire.PollStateBody{
			PollID: "p1", ChatID: "c1", Question: "Lunch?",
			Anonymous: true, MyVotes: []int32{0, 2},
		},
	)); err != nil {
		t.Fatal(err)
	}

	if len(captured.MyVotes) != 0 {
		t.Errorf("the broadcast carried a member's own votes %v — an anonymous poll would leak", captured.MyVotes)
	}
}

func TestPollStateSendsNoPush(t *testing.T) {
	// The poll's QUESTION was an ordinary message and already notified; a push
	// per vote would make an active poll unbearable.
	svc, bus, _ := newFanout([]string{"u1", "u2"})

	if err := svc.onPollState(context.Background(), event(eventbus.SubjPollState,
		wire.PollStateBody{PollID: "p1", ChatID: "c1"},
	)); err != nil {
		t.Fatal(err)
	}

	if got := bus.bySubject(eventbus.SubjNotifyPush); len(got) != 0 {
		t.Errorf("a poll tally queued %d push jobs", len(got))
	}
}

// -------------------------------------------------------------------- calls

/*
 * A call roster is not chat-wide news: it names who is talking to whom. Fanning
 * it to the whole group would disclose that two members are in a call, which the
 * chat has no business knowing.
 */
func TestCallStateReachesOnlyItsParticipants(t *testing.T) {
	svc, _, rtr := newFanout([]string{"u1", "u2", "bystander"})

	if err := svc.onCallState(context.Background(), event(eventbus.SubjCallState,
		wire.CallStateBody{
			CallID: "call-1", ChatID: "c1", InitiatorID: "u1", Kind: "audio", State: "ringing",
			Participants: []wire.CallParticipant{
				{UserID: "u1", State: "joined"},
				{UserID: "u2", State: "invited"},
			},
		},
	)); err != nil {
		t.Fatal(err)
	}

	reached := rtr.reached()
	if reached["bystander"] {
		t.Error("a chat member who is not in the call learned about it")
	}
	for _, want := range []string{"u1", "u2"} {
		if !reached[want] {
			t.Errorf("%s is in the call but did not receive its state", want)
		}
	}
}

func TestCallStateStopsRingingSomeoneWhoLeftOrDeclined(t *testing.T) {
	// Otherwise declining a call would keep delivering its roster updates — and,
	// for an offline invitee, keep pushing.
	svc, _, rtr := newFanout([]string{"u1", "u2", "u3"})

	if err := svc.onCallState(context.Background(), event(eventbus.SubjCallState,
		wire.CallStateBody{
			CallID: "call-1", ChatID: "c1", InitiatorID: "u1", State: "active",
			Participants: []wire.CallParticipant{
				{UserID: "u1", State: "joined"},
				{UserID: "u2", State: "declined"},
				{UserID: "u3", State: "left"},
			},
		},
	)); err != nil {
		t.Fatal(err)
	}

	reached := rtr.reached()
	if reached["u2"] {
		t.Error("someone who declined is still receiving the call roster")
	}
	if reached["u3"] {
		t.Error("someone who left is still receiving the call roster")
	}
}

/*
 * The push is what makes a phone actually ring. It is deliberately narrow:
 * only an INVITED participant, only while the room is still RINGING, and only
 * when the routed delivery reached no live connection.
 */
func TestAnOfflineInviteeIsPushedSoTheirPhoneRings(t *testing.T) {
	svc, bus, _ := newFanout([]string{"u1", "u2"}, "u1") // u2 is offline

	if err := svc.onCallState(context.Background(), event(eventbus.SubjCallState,
		wire.CallStateBody{
			CallID: "call-1", ChatID: "c1", InitiatorID: "u1", Kind: "video", State: "ringing",
			Participants: []wire.CallParticipant{
				{UserID: "u1", State: "joined"},
				{UserID: "u2", State: "invited"},
			},
		},
	)); err != nil {
		t.Fatal(err)
	}

	pushes := bus.bySubject(eventbus.SubjNotifyPush)
	if len(pushes) != 1 {
		t.Fatalf("queued %d call pushes, want 1 for the offline invitee", len(pushes))
	}
	if pushes[0].Key != "u2" {
		t.Errorf("push addressed to %q, want the offline invitee", pushes[0].Key)
	}
}

func TestAnOnlineInviteeIsNotPushed(t *testing.T) {
	// Their device is already receiving the ring over the socket; a push on top
	// would ring twice.
	svc, bus, _ := newFanout([]string{"u1", "u2"}, "u1", "u2")

	if err := svc.onCallState(context.Background(), event(eventbus.SubjCallState,
		wire.CallStateBody{
			CallID: "call-1", ChatID: "c1", InitiatorID: "u1", Kind: "audio", State: "ringing",
			Participants: []wire.CallParticipant{
				{UserID: "u1", State: "joined"},
				{UserID: "u2", State: "invited"},
			},
		},
	)); err != nil {
		t.Fatal(err)
	}

	if got := bus.bySubject(eventbus.SubjNotifyPush); len(got) != 0 {
		t.Errorf("an online invitee was pushed %d times", len(got))
	}
}

func TestNoPushOnceTheCallIsNoLongerRinging(t *testing.T) {
	// A roster change mid-call must not re-ring an offline device that already
	// missed the invitation.
	svc, bus, _ := newFanout([]string{"u1", "u2"}, "u1")

	if err := svc.onCallState(context.Background(), event(eventbus.SubjCallState,
		wire.CallStateBody{
			CallID: "call-1", ChatID: "c1", InitiatorID: "u1", Kind: "audio", State: "active",
			Participants: []wire.CallParticipant{
				{UserID: "u1", State: "joined"},
				{UserID: "u2", State: "invited"},
			},
		},
	)); err != nil {
		t.Fatal(err)
	}

	if got := bus.bySubject(eventbus.SubjNotifyPush); len(got) != 0 {
		t.Errorf("an active call queued %d ring pushes", len(got))
	}
}

func TestAnOfflineParticipantWhoAlreadyJoinedIsNotPushed(t *testing.T) {
	// "joined" means they answered somewhere; ringing them again is noise.
	svc, bus, _ := newFanout([]string{"u1", "u2"}, "u1")

	if err := svc.onCallState(context.Background(), event(eventbus.SubjCallState,
		wire.CallStateBody{
			CallID: "call-1", ChatID: "c1", InitiatorID: "u1", Kind: "audio", State: "ringing",
			Participants: []wire.CallParticipant{
				{UserID: "u1", State: "joined"},
				{UserID: "u2", State: "joined"},
			},
		},
	)); err != nil {
		t.Fatal(err)
	}

	if got := bus.bySubject(eventbus.SubjNotifyPush); len(got) != 0 {
		t.Errorf("a joined participant was pushed %d times", len(got))
	}
}

func TestCallPushNamesTheCall(t *testing.T) {
	// The worker needs the call id to build a ring notification rather than a
	// message one, and the initiator to say who is calling.
	svc, bus, _ := newFanout([]string{"u1", "u2"}, "u1")

	if err := svc.onCallState(context.Background(), event(eventbus.SubjCallState,
		wire.CallStateBody{
			CallID: "call-1", ChatID: "c1", InitiatorID: "u1", Kind: "video", State: "ringing",
			Participants: []wire.CallParticipant{{UserID: "u2", State: "invited"}},
		},
	)); err != nil {
		t.Fatal(err)
	}

	pushes := bus.bySubject(eventbus.SubjNotifyPush)
	if len(pushes) != 1 {
		t.Fatalf("queued %d pushes, want 1", len(pushes))
	}
	// The job is JSON on the wire — the notify worker decodes it with
	// encoding/json into its own PushJob, so that is the shape to assert.
	var job struct {
		UserID  string `json:"user_id"`
		ChatID  string `json:"chat_id"`
		CallID  string `json:"call_id"`
		Preview string `json:"preview"`
	}
	if err := json.Unmarshal(pushes[0].Data, &job); err != nil {
		t.Fatalf("the push job is not decodable as JSON: %v", err)
	}
	if job.CallID != "call-1" {
		t.Errorf("push job has no call id: %+v", job)
	}
	if !strings.Contains(job.Preview, "video") {
		t.Errorf("preview %q does not name the call kind", job.Preview)
	}
}

func TestCallStateRejectsAMalformedBody(t *testing.T) {
	svc, _, _ := newFanout([]string{"u1"})

	if err := svc.onCallState(context.Background(), eventbus.Event{
		Subject: eventbus.SubjCallState, Data: []byte("not a valid body"),
	}); err == nil {
		t.Error("a malformed event was accepted")
	}
}

// ------------------------------------------------------------- RouteSecret

func TestRouteSecretTargetsOneDevice(t *testing.T) {
	/*
	 * Secret chats are per DEVICE, not per user: each of a peer's devices runs
	 * its own ratchet, and a ciphertext sealed for one cannot be opened by
	 * another. Broadcasting to the user would deliver undecryptable frames to
	 * every other device they own.
	 */
	svc, bus, _ := newFanout([]string{"u1", "u2"}, "u2")

	svc.RouteSecret(context.Background(), "u2", "device-7", wire.SecretMsgBody{
		ToUserID: "u2", ToDeviceID: "device-7", FromUserID: "u1", Ciphertext: "opaque",
	})

	// The device id rides inside the NodeDelivery envelope the owning node
	// receives, not in the router lookup — that is keyed by user alone.
	bus.mu.Lock()
	events := append([]eventbus.Event(nil), bus.events...)
	bus.mu.Unlock()
	if len(events) != 1 {
		t.Fatalf("published %d deliveries, want 1", len(events))
	}

	nd, err := router.DecodeNodeDelivery(events[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	if len(nd.Users) != 1 || nd.Users[0] != "u2" || nd.DeviceID != "device-7" {
		t.Errorf("routed to users=%v device=%q, want [u2]/device-7", nd.Users, nd.DeviceID)
	}
	if nd.Type != uint16(wire.MsgSecretRecv) {
		t.Errorf("type = %d, want MsgSecretRecv", nd.Type)
	}
}

// ------------------------------------------------------------------ preview

/*
 * The cut is by rune, not by byte. Slicing mid-character hands the push provider
 * invalid UTF-8 — a JSON encoding error at best, a mangled notification at worst
 * — and the first users to hit it would be everyone who does not type in ASCII.
 */
func TestPreviewLeavesShortTextAlone(t *testing.T) {
	if got := preview("hello"); got != "hello" {
		t.Errorf("preview = %q", got)
	}
}

func TestPreviewTrimsLongText(t *testing.T) {
	long := strings.Repeat("a", 500)
	got := preview(long)
	if len(got) > 120 {
		t.Errorf("preview is %d bytes, want at most 120", len(got))
	}
}

func TestPreviewNeverSplitsARune(t *testing.T) {
	// Cyrillic is two bytes per character, so a byte-wise cut at 120 lands
	// mid-character for a large share of offsets.
	for _, text := range []string{
		strings.Repeat("я", 200),
		strings.Repeat("🔐", 100),
		strings.Repeat("日", 200),
		strings.Repeat("a", 119) + strings.Repeat("я", 50),
	} {
		got := preview(text)
		if !utf8ValidString(got) {
			t.Errorf("preview of %q… produced invalid UTF-8", text[:10])
		}
	}
}

func TestPreviewOfAnEmptyMessageIsEmpty(t *testing.T) {
	// A media-only message has no text; the worker renders its own placeholder.
	if got := preview(""); got != "" {
		t.Errorf("preview = %q", got)
	}
}

func TestPreviewKeepsTextExactlyAtTheLimit(t *testing.T) {
	exact := strings.Repeat("a", 120)
	if got := preview(exact); got != exact {
		t.Errorf("text at exactly the limit was trimmed to %d bytes", len(got))
	}
}

// ------------------------------------------------------------------ doubles

// payloadCapturingRouter hands the routed payload to a callback so a test can
// assert what was actually broadcast rather than what was passed in.
type payloadCapturingRouter struct {
	onPayload func([]byte)
}

func (r *payloadCapturingRouter) NodesFor(context.Context, string) ([]string, error) {
	return nil, nil
}
func (r *payloadCapturingRouter) NodesForMany(context.Context, []string) (map[string][]string, error) {
	return map[string][]string{}, nil
}
func (r *payloadCapturingRouter) NodesForDevice(context.Context, string, string) ([]string, error) {
	return nil, nil
}
func (r *payloadCapturingRouter) Bind(context.Context, string, string, string) error   { return nil }
func (r *payloadCapturingRouter) Unbind(context.Context, string, string, string) error { return nil }
func (r *payloadCapturingRouter) Refresh(context.Context, string, string) error        { return nil }

func utf8ValidString(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

// ---------------------------------------------------- push job encoding

/*
 * The staged job has to be decodable by internal/notify.onPush, which uses
 * encoding/json into its own PushJob. That contract used to be broken in a way
 * nothing reported: the job was built as a map[string]any and encoded with
 * wire.Marshal, which is protobuf-backed and has no mapping for a map — so it
 * returned an error that `b, _ :=` discarded and published ZERO BYTES. The
 * worker then failed on an empty payload and no push was ever delivered.
 *
 * These tests decode the staged bytes exactly as the worker does, so the whole
 * path is pinned rather than just the publish.
 */

// decodeAsNotifyWorker mirrors internal/notify.onPush's decode step.
func decodeAsNotifyWorker(t *testing.T, data []byte) notifyPushJob {
	t.Helper()
	var job notifyPushJob
	if err := json.Unmarshal(data, &job); err != nil {
		t.Fatalf("the notify worker cannot decode this job (%d bytes): %v", len(data), err)
	}
	return job
}

// notifyPushJob is the shape internal/notify.PushJob declares.
type notifyPushJob struct {
	UserID    string `json:"user_id"`
	ChatID    string `json:"chat_id"`
	MessageID string `json:"message_id"`
	CallID    string `json:"call_id"`
	SenderID  string `json:"sender_id"`
	Preview   string `json:"preview"`
}

func TestMessagePushJobIsDecodableByTheWorker(t *testing.T) {
	svc, bus, _ := newFanout([]string{"u1", "u2"})

	svc.enqueuePush(context.Background(), "u2", wire.NewMessageBody{
		MessageID: "m1", ChatID: "c1", SenderID: "u1", Text: "hello",
	})

	jobs := bus.bySubject(eventbus.SubjNotifyPush)
	if len(jobs) != 1 {
		t.Fatalf("staged %d push jobs, want 1", len(jobs))
	}
	if len(jobs[0].Data) == 0 {
		t.Fatal("the push job is empty; no notification could ever be delivered")
	}

	job := decodeAsNotifyWorker(t, jobs[0].Data)
	if job.UserID != "u2" || job.ChatID != "c1" || job.SenderID != "u1" {
		t.Errorf("job = %+v", job)
	}
	if job.MessageID != "m1" {
		t.Errorf("message id = %q; the worker could not link the push to its message", job.MessageID)
	}
	// No preview by default. The text used to be included unconditionally, and the
	// endpoint that receives it belongs to Apple or Google — so a third party saw
	// the contents of every conversation on the system.
	if job.Preview != "" {
		t.Errorf("preview = %q; message text must not reach the provider unless the "+
			"RECIPIENT opted in", job.Preview)
	}
}

// TestPushPreviewIsIncludedOnlyForOptedInRecipients is the other half: previews
// are not removed, they are made a choice. Someone who asked for them gets them.
func TestPushPreviewIsIncludedOnlyForOptedInRecipients(t *testing.T) {
	svc, bus, _ := newFanout([]string{"u1", "u2", "u3"})
	// u2 wants previews; u3 does not.
	svc = svc.WithPreviewPolicy(previewOptIn{"u2": true})

	svc.enqueuePush(context.Background(), "u2", wire.NewMessageBody{
		MessageID: "m1", ChatID: "c1", SenderID: "u1", Text: "hello",
	})
	svc.enqueuePush(context.Background(), "u3", wire.NewMessageBody{
		MessageID: "m1", ChatID: "c1", SenderID: "u1", Text: "hello",
	})

	jobs := bus.bySubject(eventbus.SubjNotifyPush)
	if len(jobs) != 2 {
		t.Fatalf("staged %d push jobs, want 2", len(jobs))
	}
	byUser := map[string]string{}
	for _, j := range jobs {
		job := decodeAsNotifyWorker(t, j.Data)
		byUser[job.UserID] = job.Preview
	}
	if byUser["u2"] != "hello" {
		t.Errorf("an opted-in recipient got preview %q, want the text", byUser["u2"])
	}
	if byUser["u3"] != "" {
		t.Errorf("a recipient who did not opt in got preview %q", byUser["u3"])
	}
}

// previewOptIn is a PreviewPolicy backed by a fixed set of consenting users.
type previewOptIn map[string]bool

func (p previewOptIn) WantsPushPreview(_ context.Context, userID string) bool {
	return p[userID]
}

func TestCallPushJobIsDecodableByTheWorker(t *testing.T) {
	svc, bus, _ := newFanout([]string{"u1", "u2"})

	svc.enqueueCallPush(context.Background(), "u2", wire.CallStateBody{
		CallID: "call-1", ChatID: "c1", InitiatorID: "u1", Kind: "video", State: "ringing",
	})

	jobs := bus.bySubject(eventbus.SubjNotifyPush)
	if len(jobs) != 1 {
		t.Fatalf("staged %d push jobs, want 1", len(jobs))
	}

	job := decodeAsNotifyWorker(t, jobs[0].Data)
	if job.CallID != "call-1" {
		t.Errorf("call id = %q; the worker could not tell this from a message push", job.CallID)
	}
	if job.SenderID != "u1" {
		t.Errorf("sender = %q, want the call initiator", job.SenderID)
	}
}

func TestPushJobIsAddressedToTheRecipient(t *testing.T) {
	// The bus key partitions the job, and the body names who it is for; the two
	// disagreeing would deliver one user's notification to another's worker
	// shard.
	svc, bus, _ := newFanout([]string{"u1", "u2"})

	svc.enqueuePush(context.Background(), "u2", wire.NewMessageBody{
		MessageID: "m1", ChatID: "c1", SenderID: "u1", Text: "hi",
	})

	jobs := bus.bySubject(eventbus.SubjNotifyPush)
	if jobs[0].Key != "u2" {
		t.Errorf("bus key = %q, want the recipient", jobs[0].Key)
	}
	if decodeAsNotifyWorker(t, jobs[0].Data).UserID != "u2" {
		t.Error("the job body names a different user than the bus key")
	}
}

func TestPushPreviewIsTrimmedInTheJob(t *testing.T) {
	// The trim has to happen before the job is staged, not in the worker: the
	// bus carries every push in the system, and a full message body per job is
	// the difference between a notification and a second copy of the chat log.
	svc, bus, _ := newFanout([]string{"u1", "u2"})

	svc.enqueuePush(context.Background(), "u2", wire.NewMessageBody{
		MessageID: "m1", ChatID: "c1", SenderID: "u1", Text: strings.Repeat("a", 500),
	})

	job := decodeAsNotifyWorker(t, bus.bySubject(eventbus.SubjNotifyPush)[0].Data)
	if len(job.Preview) > 120 {
		t.Errorf("preview is %d bytes, want it trimmed before staging", len(job.Preview))
	}
}

func TestPushPreviewSurvivesNonAsciiText(t *testing.T) {
	// JSON encoding of a string cut mid-rune is exactly where the old path would
	// have failed loudest had it produced any bytes at all.
	svc, bus, _ := newFanout([]string{"u1", "u2"})

	svc.enqueuePush(context.Background(), "u2", wire.NewMessageBody{
		MessageID: "m1", ChatID: "c1", SenderID: "u1", Text: strings.Repeat("я", 200),
	})

	job := decodeAsNotifyWorker(t, bus.bySubject(eventbus.SubjNotifyPush)[0].Data)
	if !utf8ValidString(job.Preview) {
		t.Error("the preview round-tripped through JSON as invalid UTF-8")
	}
}
