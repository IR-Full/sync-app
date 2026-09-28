package message

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/chat"
	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
	"github.com/SyncApp-chat/SyncApp/internal/store/memory"
	"github.com/SyncApp-chat/SyncApp/pkg/eventbus"
	"github.com/SyncApp-chat/SyncApp/pkg/id"
	"github.com/SyncApp-chat/SyncApp/pkg/wire"
)

// fixture is a message service wired to the in-memory store, plus a group chat
// with one member and one outsider.
//
// The outsider matters: every read path here is an authorization boundary, and
// the failure mode of a missing check is not a crash but a stranger quietly
// reading a group's history.
type fixture struct {
	svc     *Service
	bus     eventbus.Bus
	chats   *chat.Service
	ids     *id.Generator
	store   *memory.Store
	chatID  string
	member  string
	other   string
	outside string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ids, _ := id.NewGenerator(1)
	st := memory.New()
	stores := st.Stores()
	chatSvc := chat.New(stores.Chats, ids)
	bus := eventbus.NewMemory()
	svc := New(stores.Messages, stores.Reads, chatSvc, bus, ids)

	member := ids.NextString()
	other := ids.NextString()
	outside := ids.NextString()

	ch, err := chatSvc.CreateGroup(context.Background(), member, "Team", model.ChatGroup, []string{other})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	return &fixture{
		svc: svc, bus: bus, chats: chatSvc, ids: ids, store: st,
		chatID: ch.ID, member: member, other: other, outside: outside,
	}
}

func (f *fixture) send(t *testing.T, sender, text string) *model.Message {
	t.Helper()
	m, _, err := f.svc.Send(context.Background(), SendInput{
		SenderID: sender, ChatID: f.chatID, DedupKey: f.ids.NextString(), Text: text,
	})
	if err != nil {
		t.Fatalf("send %q: %v", text, err)
	}
	return m
}

// ----------------------------------------------------------------- History

func TestHistoryReturnsNewestFirst(t *testing.T) {
	f := newFixture(t)
	for _, text := range []string{"one", "two", "three"} {
		f.send(t, f.member, text)
	}

	page, err := f.svc.History(context.Background(), f.member, f.chatID, 0, 10)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(page) != 3 {
		t.Fatalf("want 3 messages, got %d", len(page))
	}
	if page[0].Text != "three" {
		t.Errorf("history must page backwards from the newest; got %q first", page[0].Text)
	}
}

func TestHistoryRefusesANonMember(t *testing.T) {
	// The whole point of the check. A group's history is readable by its members
	// and nobody else, and the store has no idea who is allowed.
	f := newFixture(t)
	f.send(t, f.member, "private")

	if _, err := f.svc.History(context.Background(), f.outside, f.chatID, 0, 10); !errors.Is(err, ErrForbidden) {
		t.Fatalf("an outsider read the history: err=%v", err)
	}
}

func TestHistoryAllowsEveryMember(t *testing.T) {
	f := newFixture(t)
	f.send(t, f.member, "hello")

	page, err := f.svc.History(context.Background(), f.other, f.chatID, 0, 10)
	if err != nil {
		t.Fatalf("a member was refused: %v", err)
	}
	if len(page) != 1 {
		t.Errorf("want 1 message, got %d", len(page))
	}
}

func TestHistoryPagesBackwardsFromACursor(t *testing.T) {
	// `beforeSeq` is how a client walks into the past; ignoring it would make
	// every page identical and scrolling up would loop forever.
	f := newFixture(t)
	for _, text := range []string{"one", "two", "three"} {
		f.send(t, f.member, text)
	}

	page, err := f.svc.History(context.Background(), f.member, f.chatID, 3, 10)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	for _, m := range page {
		if m.Seq >= 3 {
			t.Errorf("seq %d should be older than the cursor 3", m.Seq)
		}
	}
}

func TestHistoryHonoursTheLimit(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 5; i++ {
		f.send(t, f.member, "m")
	}

	page, err := f.svc.History(context.Background(), f.member, f.chatID, 0, 2)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(page) > 2 {
		t.Errorf("limit ignored: got %d messages", len(page))
	}
}

func TestHistoryOfAnEmptyChatIsEmpty(t *testing.T) {
	f := newFixture(t)

	page, err := f.svc.History(context.Background(), f.member, f.chatID, 0, 10)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(page) != 0 {
		t.Errorf("want no messages, got %d", len(page))
	}
}

// ---------------------------------------------------------------- MarkRead

func TestMarkReadStoresTheCursor(t *testing.T) {
	f := newFixture(t)
	m := f.send(t, f.member, "hello")

	if err := f.svc.MarkRead(context.Background(), f.other, f.chatID, m.Seq); err != nil {
		t.Fatalf("mark read: %v", err)
	}

	rs, err := f.store.Stores().Reads.GetRead(context.Background(), f.chatID, f.other)
	if err != nil {
		t.Fatalf("get read: %v", err)
	}
	if rs.UpToSeq != m.Seq {
		t.Errorf("cursor = %d, want %d", rs.UpToSeq, m.Seq)
	}
}

func TestMarkReadPublishesAReceipt(t *testing.T) {
	// The cursor is stored for this user; the *event* is what puts a read tick on
	// the sender's screen. Storing without publishing loses the whole feature
	// while every direct read of the store still looks correct.
	f := newFixture(t)
	received := f.watch(t, eventbus.SubjMessageRead)

	m := f.send(t, f.member, "hello")
	if err := f.svc.MarkRead(context.Background(), f.other, f.chatID, m.Seq); err != nil {
		t.Fatalf("mark read: %v", err)
	}

	e := awaitEvent(t, received, "no read receipt was published — the sender would never see a read tick")
	var body wire.ReadUpdateBody
	if err := wire.Unmarshal(e.Data, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.UserID != f.other || body.ChatID != f.chatID || body.UpToChatSeq != m.Seq {
		t.Errorf("receipt = %+v, want chat=%s user=%s seq=%d", body, f.chatID, f.other, m.Seq)
	}
}

func TestMarkReadKeysTheEventOnTheChat(t *testing.T) {
	// Fanout partitions by key; a receipt keyed on anything else would be
	// delivered to the wrong subscribers or out of order with the chat's traffic.
	f := newFixture(t)
	received := f.watch(t, eventbus.SubjMessageRead)

	if err := f.svc.MarkRead(context.Background(), f.other, f.chatID, 1); err != nil {
		t.Fatalf("mark read: %v", err)
	}

	if e := awaitEvent(t, received, "no event"); e.Key != f.chatID {
		t.Errorf("event key = %q, want the chat id %q", e.Key, f.chatID)
	}
}

func TestMarkReadRefusesANonMember(t *testing.T) {
	// A read cursor for a stranger would also broadcast their id to the chat.
	f := newFixture(t)

	if err := f.svc.MarkRead(context.Background(), f.outside, f.chatID, 1); !errors.Is(err, ErrForbidden) {
		t.Fatalf("an outsider marked a chat read: err=%v", err)
	}
}

func TestMarkReadPublishesNothingForANonMember(t *testing.T) {
	f := newFixture(t)
	received := f.watch(t, eventbus.SubjMessageRead)

	_ = f.svc.MarkRead(context.Background(), f.outside, f.chatID, 1)

	expectNoEvent(t, received, "a refused mark-read still published an event")
}

// ------------------------------------------------------------------ Thread

func TestThreadReturnsRepliesOldestFirst(t *testing.T) {
	// Threads read forward — a conversation branch grows downward — unlike
	// history, which pages backward. Getting this the wrong way round renders a
	// reply chain upside down.
	f := newFixture(t)
	root := f.send(t, f.member, "root")

	ctx := context.Background()
	for _, text := range []string{"first reply", "second reply"} {
		if _, _, err := f.svc.Send(ctx, SendInput{
			SenderID: f.other, ChatID: f.chatID, DedupKey: f.ids.NextString(),
			Text: text, ReplyTo: root.ID,
		}); err != nil {
			t.Fatalf("reply: %v", err)
		}
	}

	replies, err := f.svc.Thread(ctx, f.member, f.chatID, root.ID, 0, 10)
	if err != nil {
		t.Fatalf("thread: %v", err)
	}
	if len(replies) < 2 {
		t.Fatalf("want 2 replies, got %d", len(replies))
	}
	if replies[0].Text != "first reply" {
		t.Errorf("threads must read oldest first; got %q first", replies[0].Text)
	}
}

func TestThreadRefusesANonMember(t *testing.T) {
	f := newFixture(t)
	root := f.send(t, f.member, "root")

	if _, err := f.svc.Thread(context.Background(), f.outside, f.chatID, root.ID, 0, 10); !errors.Is(err, ErrForbidden) {
		t.Fatalf("an outsider read a thread: err=%v", err)
	}
}

func TestReplyingToATopLevelMessageRootsTheThreadAtIt(t *testing.T) {
	// Computed server-side so a whole branch shares ONE root: a thread is then a
	// single indexed read instead of a recursive walk, and a client cannot forge
	// a root it has no access to.
	f := newFixture(t)
	root := f.send(t, f.member, "root")

	reply, _, err := f.svc.Send(context.Background(), SendInput{
		SenderID: f.other, ChatID: f.chatID, DedupKey: f.ids.NextString(),
		Text: "reply", ReplyTo: root.ID,
	})
	if err != nil {
		t.Fatalf("reply: %v", err)
	}

	if reply.ThreadRoot != root.ID {
		t.Errorf("thread root = %q, want the parent %q", reply.ThreadRoot, root.ID)
	}
}

func TestReplyingInsideAThreadJoinsThatThread(t *testing.T) {
	// A reply to a reply must not start a second thread rooted on the middle
	// message, or the branch splits and half of it disappears from the panel.
	f := newFixture(t)
	root := f.send(t, f.member, "root")
	ctx := context.Background()

	first, _, err := f.svc.Send(ctx, SendInput{
		SenderID: f.other, ChatID: f.chatID, DedupKey: f.ids.NextString(),
		Text: "first", ReplyTo: root.ID,
	})
	if err != nil {
		t.Fatalf("first reply: %v", err)
	}

	second, _, err := f.svc.Send(ctx, SendInput{
		SenderID: f.member, ChatID: f.chatID, DedupKey: f.ids.NextString(),
		Text: "second", ReplyTo: first.ID,
	})
	if err != nil {
		t.Fatalf("second reply: %v", err)
	}

	if second.ThreadRoot != root.ID {
		t.Errorf("thread root = %q, want the original root %q", second.ThreadRoot, root.ID)
	}
}

func TestATopLevelMessageHasNoThreadRoot(t *testing.T) {
	f := newFixture(t)
	m := f.send(t, f.member, "standalone")

	if m.ThreadRoot != "" {
		t.Errorf("thread root = %q, want empty", m.ThreadRoot)
	}
}

func TestReplyingToAMissingMessageStillPosts(t *testing.T) {
	// The parent lookup is best-effort: a reply whose target was deleted, or
	// whose id a client got wrong, must still be delivered rather than rejected.
	f := newFixture(t)

	m, _, err := f.svc.Send(context.Background(), SendInput{
		SenderID: f.member, ChatID: f.chatID, DedupKey: f.ids.NextString(),
		Text: "reply to nothing", ReplyTo: "does-not-exist",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if m.ThreadRoot != "" {
		t.Errorf("thread root = %q, want empty for an unresolvable parent", m.ThreadRoot)
	}
}

// ----------------------------------------------------------------- Forward

func TestForwardCopiesTheContent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	src := f.send(t, f.member, "original text")

	dst, err := f.chats.CreateGroup(ctx, f.member, "Elsewhere", model.ChatGroup, nil)
	if err != nil {
		t.Fatalf("create destination: %v", err)
	}

	copied, _, err := f.svc.Forward(ctx, f.member, f.chatID, src.ID, dst.ID, f.ids.NextString())
	if err != nil {
		t.Fatalf("forward: %v", err)
	}

	if copied.Text != "original text" {
		t.Errorf("text = %q, want the source text", copied.Text)
	}
	if copied.ChatID != dst.ID {
		t.Errorf("chat = %q, want the destination %q", copied.ChatID, dst.ID)
	}
	if copied.ID == src.ID {
		t.Error("a forward must be an independent message, not the same row")
	}
}

func TestForwardRecordsProvenance(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	src := f.send(t, f.other, "by someone else")

	dst, err := f.chats.CreateGroup(ctx, f.member, "Elsewhere", model.ChatGroup, nil)
	if err != nil {
		t.Fatalf("create destination: %v", err)
	}

	copied, _, err := f.svc.Forward(ctx, f.member, f.chatID, src.ID, dst.ID, f.ids.NextString())
	if err != nil {
		t.Fatalf("forward: %v", err)
	}

	if copied.Forward == nil {
		t.Fatal("a forward with no provenance reads as originally written by the forwarder")
	}
	if copied.Forward.SenderID != f.other {
		t.Errorf("provenance sender = %q, want the original author %q", copied.Forward.SenderID, f.other)
	}
}

func TestForwardingAForwardKeepsTheOriginalAuthor(t *testing.T) {
	// Otherwise a chain of forwards credits whoever forwarded it last, which is
	// exactly the attribution error that makes a quote look like a fabrication.
	f := newFixture(t)
	ctx := context.Background()
	src := f.send(t, f.other, "originally by other")

	first, err := f.chats.CreateGroup(ctx, f.member, "First hop", model.ChatGroup, nil)
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := f.chats.CreateGroup(ctx, f.member, "Second hop", model.ChatGroup, nil)
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	hop1, _, err := f.svc.Forward(ctx, f.member, f.chatID, src.ID, first.ID, f.ids.NextString())
	if err != nil {
		t.Fatalf("first forward: %v", err)
	}
	hop2, _, err := f.svc.Forward(ctx, f.member, first.ID, hop1.ID, second.ID, f.ids.NextString())
	if err != nil {
		t.Fatalf("second forward: %v", err)
	}

	if hop2.Forward == nil || hop2.Forward.SenderID != f.other {
		t.Errorf("provenance = %+v, want the original author %q", hop2.Forward, f.other)
	}
	if hop2.Forward.MessageID != src.ID {
		t.Errorf("provenance message = %q, want the original %q", hop2.Forward.MessageID, src.ID)
	}
}

func TestForwardRequiresMembershipOfTheSource(t *testing.T) {
	// Reading is the privileged half here: without this check a stranger could
	// copy a private chat's messages into a chat they control.
	f := newFixture(t)
	ctx := context.Background()
	src := f.send(t, f.member, "private")

	dst, err := f.chats.CreateGroup(ctx, f.outside, "Attacker's chat", model.ChatGroup, nil)
	if err != nil {
		t.Fatalf("create destination: %v", err)
	}

	if _, _, err := f.svc.Forward(ctx, f.outside, f.chatID, src.ID, dst.ID, f.ids.NextString()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("an outsider forwarded out of a chat they are not in: err=%v", err)
	}
}

func TestForwardRequiresPermissionInTheDestination(t *testing.T) {
	// Delegated to Send, which is what makes a forward obey the same posting
	// rules as an ordinary message.
	f := newFixture(t)
	ctx := context.Background()
	src := f.send(t, f.member, "hello")

	stranger, err := f.chats.CreateGroup(ctx, f.outside, "Theirs", model.ChatGroup, nil)
	if err != nil {
		t.Fatalf("create destination: %v", err)
	}

	if _, _, err := f.svc.Forward(ctx, f.member, f.chatID, src.ID, stranger.ID, f.ids.NextString()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("forwarded into a chat the user cannot post to: err=%v", err)
	}
}

func TestForwardRefusesADeletedSource(t *testing.T) {
	// A tombstone keeps the row; copying it would resurrect content the author
	// has already retracted.
	f := newFixture(t)
	ctx := context.Background()
	src := f.send(t, f.member, "regrettable")

	if _, err := f.svc.Delete(ctx, f.member, f.chatID, src.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	dst, err := f.chats.CreateGroup(ctx, f.member, "Elsewhere", model.ChatGroup, nil)
	if err != nil {
		t.Fatalf("create destination: %v", err)
	}

	if _, _, err := f.svc.Forward(ctx, f.member, f.chatID, src.ID, dst.ID, f.ids.NextString()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("forwarded a deleted message: err=%v", err)
	}
}

func TestForwardIsIdempotentOnItsDedupKey(t *testing.T) {
	// A retry after a reconnect must not post the copy twice.
	f := newFixture(t)
	ctx := context.Background()
	src := f.send(t, f.member, "hello")

	dst, err := f.chats.CreateGroup(ctx, f.member, "Elsewhere", model.ChatGroup, nil)
	if err != nil {
		t.Fatalf("create destination: %v", err)
	}

	key := f.ids.NextString()
	first, dup1, err := f.svc.Forward(ctx, f.member, f.chatID, src.ID, dst.ID, key)
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	second, dup2, err := f.svc.Forward(ctx, f.member, f.chatID, src.ID, dst.ID, key)
	if err != nil {
		t.Fatalf("forward retry: %v", err)
	}

	if dup1 {
		t.Error("the first forward reported itself as a duplicate")
	}
	if !dup2 || second.ID != first.ID {
		t.Errorf("a retry created a second copy: dup=%v id=%s want=%s", dup2, second.ID, first.ID)
	}
}

func TestForwardCarriesTheAttachment(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	src, _, err := f.svc.Send(ctx, SendInput{
		SenderID: f.member, ChatID: f.chatID, DedupKey: f.ids.NextString(), Text: "look",
		Attachment: &model.Attachment{Kind: model.AttachImage, MediaRef: "media-1", Width: 10, Height: 20},
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	dst, err := f.chats.CreateGroup(ctx, f.member, "Elsewhere", model.ChatGroup, nil)
	if err != nil {
		t.Fatalf("create destination: %v", err)
	}

	copied, _, err := f.svc.Forward(ctx, f.member, f.chatID, src.ID, dst.ID, f.ids.NextString())
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	if copied.Attachment == nil || copied.Attachment.MediaRef != "media-1" {
		t.Errorf("attachment = %+v, want the source's media ref", copied.Attachment)
	}
}

// --------------------------------------------------------------- ExpireDue

func TestExpireDueTombstonesAMessagePastItsDeadline(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	m, _, err := f.svc.Send(ctx, SendInput{
		SenderID: f.member, ChatID: f.chatID, DedupKey: f.ids.NextString(),
		Text: "self destructs", TTLSeconds: 1,
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	n, err := f.svc.ExpireDue(ctx, m.ExpiresAt+1, 10)
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if n != 1 {
		t.Fatalf("expired %d messages, want 1", n)
	}

	after, err := f.store.Stores().Messages.GetMessage(ctx, f.chatID, m.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !after.Deleted {
		t.Error("the message survived its own deadline")
	}
}

func TestExpireDueLeavesAMessageThatIsNotDueYet(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	m, _, err := f.svc.Send(ctx, SendInput{
		SenderID: f.member, ChatID: f.chatID, DedupKey: f.ids.NextString(),
		Text: "later", TTLSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	n, err := f.svc.ExpireDue(ctx, m.CreatedAt, 10)
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if n != 0 {
		t.Errorf("expired %d messages early", n)
	}
}

func TestExpireDueIgnoresMessagesWithNoDeadline(t *testing.T) {
	// TTL 0 means "keep forever"; treating it as "expires at the epoch" would
	// delete every ordinary message on the first sweep.
	f := newFixture(t)
	f.send(t, f.member, "ordinary")

	n, err := f.svc.ExpireDue(context.Background(), 1<<62, 10)
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if n != 0 {
		t.Errorf("expired %d messages that had no deadline", n)
	}
}

func TestExpireDuePublishesADeletionPerMessage(t *testing.T) {
	// Live clients drop the content on the event. Without it a self-destructing
	// message stays on screen until something else forces a refetch — which, in
	// an open chat, may be never.
	f := newFixture(t)
	ctx := context.Background()
	received := f.watch(t, eventbus.SubjMessageDeleted)

	m, _, err := f.svc.Send(ctx, SendInput{
		SenderID: f.member, ChatID: f.chatID, DedupKey: f.ids.NextString(),
		Text: "self destructs", TTLSeconds: 1,
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if _, err := f.svc.ExpireDue(ctx, m.ExpiresAt+1, 10); err != nil {
		t.Fatalf("expire: %v", err)
	}

	e := awaitEvent(t, received, "no deletion event — clients would keep showing an expired message")
	var body wire.NewMessageBody
	if err := wire.Unmarshal(e.Data, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.MessageID != m.ID || !body.Deleted {
		t.Errorf("event = %+v, want a deletion for %s", body, m.ID)
	}
}

func TestExpireDueHonoursItsLimit(t *testing.T) {
	// The sweep runs on a timer against a shared store; an unbounded claim would
	// let one node hold every due row and stall the others.
	f := newFixture(t)
	ctx := context.Background()

	var latest int64
	for i := 0; i < 5; i++ {
		m, _, err := f.svc.Send(ctx, SendInput{
			SenderID: f.member, ChatID: f.chatID, DedupKey: f.ids.NextString(),
			Text: "x", TTLSeconds: 1,
		})
		if err != nil {
			t.Fatalf("send: %v", err)
		}
		latest = m.ExpiresAt
	}

	n, err := f.svc.ExpireDue(ctx, latest+1, 2)
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if n > 2 {
		t.Errorf("expired %d messages, limit was 2", n)
	}
}

func TestExpireDueIsANoOpOnABackendWithoutExpiry(t *testing.T) {
	// Expiry is an optional store capability; a backend without it must simply
	// never expire rather than fail the sweep on every tick.
	svc := New(noExpiryStore{}, nil, nil, eventbus.NewMemory(), mustIDs(t))

	n, err := svc.ExpireDue(context.Background(), 1<<62, 10)
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if n != 0 {
		t.Errorf("expired %d on a store that cannot expire", n)
	}
}

// ------------------------------------------------------ media collection

// recordingCollector notes which refs were offered for collection.
type recordingCollector struct{ refs []string }

func (c *recordingCollector) DeleteIfUnreferenced(_ context.Context, _ string, refs ...string) {
	c.refs = append(c.refs, refs...)
}

func TestDeleteOffersTheMessagesBlobsForCollection(t *testing.T) {
	/*
	 * The refs have to be captured BEFORE the tombstone: the store clears them on
	 * the way out, so a collector called with the returned row would be handed an
	 * empty list and every blob would leak until the periodic sweep found it.
	 */
	f := newFixture(t)
	ctx := context.Background()
	collector := &recordingCollector{}
	f.svc.WithMedia(collector)

	m, _, err := f.svc.Send(ctx, SendInput{
		SenderID: f.member, ChatID: f.chatID, DedupKey: f.ids.NextString(),
		MediaRef: "plain-ref",
		Attachment: &model.Attachment{
			Kind: model.AttachImage, MediaRef: "attachment-ref", ThumbRef: "thumb-ref",
		},
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if _, err := f.svc.Delete(ctx, f.member, f.chatID, m.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	want := map[string]bool{"plain-ref": true, "attachment-ref": true, "thumb-ref": true}
	for _, ref := range collector.refs {
		delete(want, ref)
	}
	if len(want) != 0 {
		t.Errorf("these refs were never offered for collection: %v (got %v)", want, collector.refs)
	}
}

func TestDeleteOffersNothingForATextMessage(t *testing.T) {
	f := newFixture(t)
	collector := &recordingCollector{}
	f.svc.WithMedia(collector)
	m := f.send(t, f.member, "just words")

	if _, err := f.svc.Delete(context.Background(), f.member, f.chatID, m.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if len(collector.refs) != 0 {
		t.Errorf("offered %v for collection on a message with no media", collector.refs)
	}
}

func TestDeleteWorksWithoutACollector(t *testing.T) {
	// The collector is optional — without one, blobs are collected later by the
	// periodic sweep rather than promptly.
	f := newFixture(t)
	m := f.send(t, f.member, "hello")

	if _, err := f.svc.Delete(context.Background(), f.member, f.chatID, m.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

// --------------------------------------------------- Delete authorization
//
// These four cover the boundary that the non-sender branch of Delete draws. It
// used to ask CanPost, which in a group is true for every member, so any member
// could erase any other member's messages. The tests are written around the
// ROLE rather than around the call, so they keep meaning if the implementation
// changes again.

func TestAnOrdinaryMemberCannotDeleteAnotherMembersMessage(t *testing.T) {
	f := newFixture(t)
	m := f.send(t, f.member, "written by the owner")

	// f.other is a plain member of the group: allowed to post, not to moderate.
	_, err := f.svc.Delete(context.Background(), f.other, f.chatID, m.ID)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("a plain group member deleted someone else's message; want ErrForbidden, got %v", err)
	}

	after, err := f.store.Stores().Messages.GetMessage(context.Background(), f.chatID, m.ID)
	if err != nil {
		t.Fatalf("get message: %v", err)
	}
	if after.Deleted {
		t.Error("the refusal must leave the message intact, not merely report an error")
	}
}

func TestAnAdminCanDeleteAnotherMembersMessage(t *testing.T) {
	f := newFixture(t)
	m := f.send(t, f.other, "written by a member")

	if err := f.chats.SetMemberRole(context.Background(), f.chatID, f.other, model.RoleAdmin); err != nil {
		t.Fatalf("set role: %v", err)
	}
	// Promote a THIRD party so the test cannot pass by way of the sender branch.
	if err := f.chats.AddMember(context.Background(), &model.ChatMember{
		ChatID: f.chatID, UserID: f.outside, Role: model.RoleAdmin, JoinedAt: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatalf("add admin: %v", err)
	}

	if _, err := f.svc.Delete(context.Background(), f.outside, f.chatID, m.ID); err != nil {
		t.Fatalf("an admin must be able to delete a member's message: %v", err)
	}
}

func TestTheOwnerCanDeleteAnotherMembersMessage(t *testing.T) {
	f := newFixture(t)
	m := f.send(t, f.other, "written by a member")

	// f.member created the group, so it holds RoleOwner.
	if _, err := f.svc.Delete(context.Background(), f.member, f.chatID, m.ID); err != nil {
		t.Fatalf("the owner must be able to delete a member's message: %v", err)
	}
}

func TestEitherSideOfADirectChatMayDeleteTheOthersMessage(t *testing.T) {
	// A 1:1 chat has no hierarchy to appeal to: both participants are equals, and
	// the moderation check must not lock both of them out by looking for an admin
	// that a direct chat never has.
	f := newFixture(t)
	ctx := context.Background()

	direct, err := f.chats.EnsureDirect(ctx, f.member, f.other)
	if err != nil {
		t.Fatalf("ensure direct: %v", err)
	}
	m, _, err := f.svc.Send(ctx, SendInput{
		SenderID: f.member, ChatID: direct.ID, DedupKey: f.ids.NextString(), Text: "hi",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if _, err := f.svc.Delete(ctx, f.other, direct.ID, m.ID); err != nil {
		t.Fatalf("the peer in a 1:1 chat must be able to delete: %v", err)
	}
}

func TestWithMediaReturnsTheServiceForChaining(t *testing.T) {
	f := newFixture(t)
	if got := f.svc.WithMedia(&recordingCollector{}); got != f.svc {
		t.Error("WithMedia must return the same service so it can be chained at construction")
	}
}

func TestMediaRefsOfIsNilSafe(t *testing.T) {
	if refs := mediaRefsOf(nil); len(refs) != 0 {
		t.Errorf("mediaRefsOf(nil) = %v", refs)
	}
}

func TestMediaRefsOfSkipsEmptyRefs(t *testing.T) {
	// An empty string is not a blob; offering it would make the collector do a
	// lookup for a ref that cannot exist.
	refs := mediaRefsOf(&model.Message{
		MediaRef:   "",
		Attachment: &model.Attachment{MediaRef: "real", ThumbRef: ""},
	})
	if len(refs) != 1 || refs[0] != "real" {
		t.Errorf("refs = %v, want just the real one", refs)
	}
}

// ------------------------------------------------- wire/model conversions

func TestAttachmentSurvivesTheWireRoundTrip(t *testing.T) {
	// The two types are deliberately separate so the protocol and the storage
	// schema can evolve independently — which is exactly what makes a dropped
	// field easy and invisible.
	original := &model.Attachment{
		Kind: model.AttachVoice, MediaRef: "m1", Filename: "note.ogg", MIME: "audio/ogg",
		Size: 2048, DurationMs: 3400, Waveform: []int32{1, 2, 3},
		Width: 10, Height: 20, ThumbRef: "t1",
	}

	got := ModelAttachment(WireAttachment(original))

	if got == nil {
		t.Fatal("round-trip produced nil")
	}
	if !equalAttachments(got, original) {
		t.Errorf("round-trip lost data:\nwant %+v\ngot  %+v", original, got)
	}
}

func equalAttachments(a, b *model.Attachment) bool {
	if a.Kind != b.Kind || a.MediaRef != b.MediaRef || a.Filename != b.Filename ||
		a.MIME != b.MIME || a.Size != b.Size || a.DurationMs != b.DurationMs ||
		a.Width != b.Width || a.Height != b.Height || a.ThumbRef != b.ThumbRef {
		return false
	}
	if len(a.Waveform) != len(b.Waveform) {
		return false
	}
	for i := range a.Waveform {
		if a.Waveform[i] != b.Waveform[i] {
			return false
		}
	}
	return true
}

func TestAttachmentConversionsAreNilSafe(t *testing.T) {
	// Most messages carry no attachment at all, so nil is the common input.
	if WireAttachment(nil) != nil {
		t.Error("WireAttachment(nil) invented a value")
	}
	if ModelAttachment(nil) != nil {
		t.Error("ModelAttachment(nil) invented a value")
	}
}

func TestForwardConversionIsNilSafe(t *testing.T) {
	if WireForward(nil) != nil {
		t.Error("WireForward(nil) invented a value")
	}
}

func TestForwardConvertsEveryField(t *testing.T) {
	// Provenance is three ids and nothing else; dropping any one of them makes
	// the forward unattributable.
	got := WireForward(&model.ForwardOrigin{ChatID: "c1", MessageID: "m1", SenderID: "u1"})
	if got.ChatID != "c1" || got.MessageID != "m1" || got.SenderID != "u1" {
		t.Errorf("forward = %+v", got)
	}
}

// ------------------------------------------------------------------ helpers

// watch subscribes to a subject and funnels the events into a channel.
//
// The in-process bus dispatches handlers on goroutines, so a publish is not
// observable on the calling goroutine — a non-blocking read would almost always
// run first and report "nothing was published" regardless.
func (f *fixture) watch(t *testing.T, subject string) <-chan eventbus.Event {
	t.Helper()
	events := make(chan eventbus.Event, 16)
	if err := f.bus.Subscribe(subject, "", func(_ context.Context, e eventbus.Event) error {
		events <- e
		return nil
	}); err != nil {
		t.Fatalf("subscribe %s: %v", subject, err)
	}
	return events
}

func awaitEvent(t *testing.T, events <-chan eventbus.Event, whenMissing string) eventbus.Event {
	t.Helper()
	select {
	case e := <-events:
		return e
	case <-time.After(2 * time.Second):
		t.Fatal(whenMissing)
		return eventbus.Event{}
	}
}

// expectNoEvent waits briefly rather than checking once: the dispatch is async,
// so an immediate check would pass even when an event was on its way.
func expectNoEvent(t *testing.T, events <-chan eventbus.Event, whenPresent string) {
	t.Helper()
	select {
	case e := <-events:
		t.Fatalf("%s: %+v", whenPresent, e)
	case <-time.After(150 * time.Millisecond):
	}
}

func mustIDs(t *testing.T) *id.Generator {
	t.Helper()
	ids, err := id.NewGenerator(1)
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

// noExpiryStore is a MessageStore that does not implement store.Expirer.
type noExpiryStore struct{ store.MessageStore }
