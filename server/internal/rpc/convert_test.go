package rpc

import (
	"reflect"
	"testing"

	"github.com/IR-Full/sync-app/server/internal/message"
	"github.com/IR-Full/sync-app/server/internal/model"
	pb "github.com/IR-Full/sync-app/server/internal/rpc/pb"
	"github.com/IR-Full/sync-app/server/pkg/wire"
)

// These mappers are the whole reason a split deployment can behave like the
// monolith. A field dropped in one of them is invisible in every in-process
// test — the monolith never calls these functions — and shows up only as a
// message that lost its attachment, or a chat that lost its handle, when the
// services are actually split. So the round-trip is asserted field by field,
// and fillStruct below makes a NEW field fail the test until it is mapped.

// fillStruct writes a distinct non-zero value into every exported field of a
// struct, recursing into nested structs and pointers.
//
// The point is the failure mode of a *forgotten* field: a hand-written fixture
// only covers the fields whoever wrote it remembered, so adding a field to
// model.Message and forgetting it in pbMessage would leave every existing test
// green. Here the new field gets a value automatically and the round-trip
// comparison fails.
func fillStruct(t *testing.T, value reflect.Value, seed *int) {
	t.Helper()
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		if !field.CanSet() {
			continue
		}
		*seed++
		switch field.Kind() {
		case reflect.String:
			field.SetString("v" + string(rune('A'+*seed%26)) + string(rune('0'+*seed%10)))
		case reflect.Int, reflect.Int32, reflect.Int64:
			field.SetInt(int64(1000 + *seed))
		case reflect.Uint, reflect.Uint32, reflect.Uint64:
			field.SetUint(uint64(2000 + *seed))
		case reflect.Bool:
			field.SetBool(true)
		case reflect.Slice:
			slice := reflect.MakeSlice(field.Type(), 2, 2)
			for j := 0; j < 2; j++ {
				element := slice.Index(j)
				switch element.Kind() {
				case reflect.Int32, reflect.Int64, reflect.Int:
					element.SetInt(int64(j + 1))
				case reflect.String:
					element.SetString("e" + string(rune('0'+j)))
				}
			}
			field.Set(slice)
		case reflect.Pointer:
			if field.Type().Elem().Kind() != reflect.Struct {
				continue
			}
			nested := reflect.New(field.Type().Elem())
			fillStruct(t, nested.Elem(), seed)
			field.Set(nested)
		case reflect.Struct:
			fillStruct(t, field, seed)
		}
	}
}

// requireEqual compares with reflect.DeepEqual, matching how pkg/wire's codec
// tests assert round-trips — no extra dependency for one comparison helper.
func requireEqual[T any](t *testing.T, what string, want, got T) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		t.Errorf("%s\nwant %+v\ngot  %+v", what, want, got)
	}
}

func filled[T any](t *testing.T) *T {
	t.Helper()
	value := new(T)
	seed := 0
	fillStruct(t, reflect.ValueOf(value).Elem(), &seed)
	return value
}

func TestUserRoundTrip(t *testing.T) {
	// PasswordHash is deliberately absent from pb.User — a credential has no
	// business crossing a service boundary — so it is cleared before comparing
	// rather than asserted to survive.
	original := filled[model.User](t)
	original.PasswordHash = ""

	got := modelUser(pbUser(original))

	requireEqual(t, "user round-trip lost data", original, got)
}

func TestUserNeverCarriesThePasswordHash(t *testing.T) {
	// The split deployment would otherwise ship every user's hash to whichever
	// service asked for a profile.
	original := filled[model.User](t)
	original.PasswordHash = "argon2id$secret"

	if got := modelUser(pbUser(original)); got.PasswordHash != "" {
		t.Errorf("password hash crossed the wire: %q", got.PasswordHash)
	}
}

func TestSessionRoundTrip(t *testing.T) {
	original := filled[model.Session](t)

	got := modelSession(pbSession(original))

	requireEqual(t, "session round-trip lost data", original, got)
}

func TestSessionCarriesBothTokens(t *testing.T) {
	// Token authenticates; ResumeToken is what lets a reconnect skip a full
	// re-auth and replay the buffer. Losing either one degrades silently into a
	// login loop rather than an error.
	original := &model.Session{ID: "s1", Token: "bearer", ResumeToken: "resume"}

	got := modelSession(pbSession(original))

	if got.Token != "bearer" || got.ResumeToken != "resume" {
		t.Errorf("tokens lost: %+v", got)
	}
}

func TestChatRoundTrip(t *testing.T) {
	original := filled[model.Chat](t)
	original.Type = model.ChatGroup

	got := modelChat(pbChat(original))

	requireEqual(t, "chat round-trip lost data", original, got)
}

func TestChatTypeSurvivesAsAString(t *testing.T) {
	// model.ChatType is a named string and pb.Chat.Type is a plain one; the
	// conversion has to be explicit in both directions or a group comes back
	// looking like a direct chat.
	for _, chatType := range []model.ChatType{
		model.ChatDirect, model.ChatGroup, model.ChatChannel,
	} {
		got := modelChat(pbChat(&model.Chat{ID: "c1", Type: chatType}))
		if got.Type != chatType {
			t.Errorf("chat type %q became %q", chatType, got.Type)
		}
	}
}

func TestChatSummaryRoundTrip(t *testing.T) {
	original := model.ChatSummary{
		Chat:   filled[model.Chat](t),
		MyRole: model.RoleAdmin,
		PeerID: "u2",
	}

	got := modelChatSummary(pbChatSummary(original))

	requireEqual(t, "chat summary round-trip lost data", original, got)
}

func TestChatSummaryKeepsPerUserFields(t *testing.T) {
	// MyRole and PeerID are the only things in a summary that are true for one
	// user rather than for the chat. Dropping MyRole would hide every admin
	// control; dropping PeerID would leave a 1:1 chat with no one to show.
	original := model.ChatSummary{
		Chat:   &model.Chat{ID: "c1", Type: model.ChatDirect},
		MyRole: model.RoleOwner,
		PeerID: "u2",
	}

	got := modelChatSummary(pbChatSummary(original))

	if got.MyRole != model.RoleOwner || got.PeerID != "u2" {
		t.Errorf("per-user fields lost: %+v", got)
	}
}

func TestMemberRoundTrip(t *testing.T) {
	original := filled[model.ChatMember](t)
	original.Role = model.RoleAdmin

	got := modelMember(pbMember(original))

	requireEqual(t, "member round-trip lost data", original, got)
}

func TestMemberMutedSurvives(t *testing.T) {
	// `Muted` is the one bool here, and a bool is exactly the field a mapper is
	// most likely to omit without anything looking wrong.
	got := modelMember(pbMember(&model.ChatMember{ChatID: "c1", UserID: "u1", Muted: true}))
	if !got.Muted {
		t.Error("muted flag lost — the member would start getting push again")
	}
}

func TestMessageRoundTrip(t *testing.T) {
	original := filled[model.Message](t)

	got := modelMessage(pbMessage(original))

	requireEqual(t, "message round-trip lost data", original, got)
}

func TestMessageCarriesItsAttachment(t *testing.T) {
	// The attachment is what lets a client render a voice waveform or a file
	// card without fetching the blob. Losing it turns a voice note into a blank
	// bubble on the split deployment only.
	original := &model.Message{
		ID:     "m1",
		ChatID: "c1",
		Attachment: &model.Attachment{
			Kind: model.AttachVoice, MediaRef: "media-1", Filename: "note.ogg",
			MIME: "audio/ogg", Size: 2048, DurationMs: 3400,
			Waveform: []int32{10, 40, 90}, Width: 0, Height: 0, ThumbRef: "thumb-1",
		},
	}

	got := modelMessage(pbMessage(original))

	requireEqual(t, "attachment round-trip lost data", original.Attachment, got.Attachment)
}

func TestMessageWithoutAttachmentStaysNil(t *testing.T) {
	// An empty &Attachment{} is not the same as none: a client would render an
	// attachment card for a plain text message.
	got := modelMessage(pbMessage(&model.Message{ID: "m1"}))
	if got.Attachment != nil {
		t.Errorf("nil attachment became %+v", got.Attachment)
	}
}

func TestMessageCarriesForwardProvenance(t *testing.T) {
	// Provenance is the whole point of a forward; without it the message reads
	// as originally written by whoever forwarded it.
	original := &model.Message{
		ID:      "m1",
		Forward: &model.ForwardOrigin{ChatID: "c0", MessageID: "m0", SenderID: "u9"},
	}

	got := modelMessage(pbMessage(original))

	requireEqual(t, "forward origin lost", original.Forward, got.Forward)
}

func TestMessageWithoutForwardStaysNil(t *testing.T) {
	got := modelMessage(pbMessage(&model.Message{ID: "m1"}))
	if got.Forward != nil {
		t.Errorf("nil forward became %+v", got.Forward)
	}
}

func TestMessageCarriesThreadAndExpiry(t *testing.T) {
	// ThreadRoot groups a reply chain and ExpiresAt is a self-destruct deadline.
	// A lost ExpiresAt is the worse of the two: the message simply never expires.
	original := &model.Message{
		ID: "m1", ThreadRoot: "root-1", ReplyCount: 7, ExpiresAt: 1_700_000_000_000,
	}

	got := modelMessage(pbMessage(original))

	if got.ThreadRoot != "root-1" || got.ReplyCount != 7 || got.ExpiresAt != original.ExpiresAt {
		t.Errorf("thread/expiry fields lost: %+v", got)
	}
}

func TestMessageKeepsTombstoneFlags(t *testing.T) {
	// A deleted message keeps its row; losing the flag would resurrect the text
	// in a client that re-fetched it.
	original := &model.Message{ID: "m1", Deleted: true, Edited: true, EditedAt: 42}

	got := modelMessage(pbMessage(original))

	if !got.Deleted || !got.Edited || got.EditedAt != 42 {
		t.Errorf("tombstone flags lost: %+v", got)
	}
}

func TestAttachmentKindSurvivesAsAString(t *testing.T) {
	for _, kind := range []model.AttachmentKind{
		model.AttachVoice, model.AttachVideoNote, model.AttachFile, model.AttachImage,
	} {
		got := modelAttachment(pbAttachment(&model.Attachment{Kind: kind, MediaRef: "m"}))
		if got.Kind != kind {
			t.Errorf("attachment kind %q became %q", kind, got.Kind)
		}
	}
}

func TestAttachmentWaveformSurvives(t *testing.T) {
	// A repeated field is the other classic omission, and an empty waveform
	// renders a voice note as a flat bar rather than as an error.
	original := &model.Attachment{MediaRef: "m", Waveform: []int32{0, 50, 100, 25}}

	got := modelAttachment(pbAttachment(original))

	requireEqual(t, "waveform lost", original.Waveform, got.Waveform)
}

func TestNilInputsStayNil(t *testing.T) {
	// Every mapper guards nil because the services genuinely pass one — a chat
	// that does not exist, a message with no attachment. A missing guard is a
	// nil dereference that takes the whole service down.
	if pbUser(nil) != nil || modelUser(nil) != nil {
		t.Error("user mapper invented a value from nil")
	}
	if pbSession(nil) != nil || modelSession(nil) != nil {
		t.Error("session mapper invented a value from nil")
	}
	if pbChat(nil) != nil || modelChat(nil) != nil {
		t.Error("chat mapper invented a value from nil")
	}
	if pbMember(nil) != nil || modelMember(nil) != nil {
		t.Error("member mapper invented a value from nil")
	}
	if pbMessage(nil) != nil || modelMessage(nil) != nil {
		t.Error("message mapper invented a value from nil")
	}
	if pbAttachment(nil) != nil || modelAttachment(nil) != nil {
		t.Error("attachment mapper invented a value from nil")
	}
	if pbForward(nil) != nil || modelForward(nil) != nil {
		t.Error("forward mapper invented a value from nil")
	}
}

func TestKeyBundleRoundTrip(t *testing.T) {
	// Every field here is key material. A dropped SignedPreKeySig would disable
	// the MITM check silently; a dropped OneTimePreKey would quietly downgrade
	// X3DH to the three-DH variant.
	original := wire.KeyBundleBody{
		UserID: "u1", DeviceID: "d1", IdentityKey: "ik", SigningKey: "sk",
		SignedPreKey: "spk", SignedPreKeySig: "sig", OneTimePreKey: "opk",
	}

	got := wireKeyBundle(pbKeyBundle(original))

	requireEqual(t, "key bundle round-trip lost data", original, got)
}

func TestKeyBundleWithoutOneTimePreKey(t *testing.T) {
	// A device whose pool is exhausted publishes a bundle without one; the
	// mapping must not turn that into a bundle claiming a key it does not have.
	original := wire.KeyBundleBody{UserID: "u1", DeviceID: "d1", IdentityKey: "ik"}

	if got := wireKeyBundle(pbKeyBundle(original)); got.OneTimePreKey != "" {
		t.Errorf("invented a one-time prekey: %q", got.OneTimePreKey)
	}
}

func TestNilKeyBundleIsEmptyNotNil(t *testing.T) {
	// wire.KeyBundleBody is a value type, so the nil case has to produce a
	// zero value rather than panic.
	if got := wireKeyBundle(nil); got != (wire.KeyBundleBody{}) {
		t.Errorf("nil bundle became %+v", got)
	}
}

func TestOpMappingIsABijection(t *testing.T) {
	// Op decides whether a command creates, edits or deletes. A collision here
	// would turn an edit into a delete on the split deployment.
	seen := make(map[pb.Op]message.Op, len(opToPB))
	for op, encoded := range opToPB {
		if previous, clash := seen[encoded]; clash {
			t.Fatalf("ops %v and %v both encode to %v", previous, op, encoded)
		}
		seen[encoded] = op

		if back, ok := opFromPB[encoded]; !ok || back != op {
			t.Errorf("op %v did not round-trip (got %v, present=%v)", op, back, ok)
		}
	}
}

func TestEveryOpHasAnEncoding(t *testing.T) {
	for _, op := range []message.Op{message.OpCreate, message.OpEdit, message.OpDelete} {
		if _, ok := opToPB[op]; !ok {
			t.Errorf("op %v has no protobuf encoding — it would decode as OP_CREATE", op)
		}
	}
}

// TestFillStructLeavesNoFieldZero guards the guard.
//
// The round-trip tests above are only as good as fillStruct: a field whose kind
// it does not handle stays at its zero value, which round-trips through *any*
// mapping — including one that drops it. Then the test that was supposed to
// catch the omission passes instead. This asserts the helper actually populates
// every exported field of the types it is used on.
func TestFillStructLeavesNoFieldZero(t *testing.T) {
	check := func(name string, value reflect.Value) {
		t.Helper()
		structType := value.Type()
		for i := 0; i < value.NumField(); i++ {
			field := structType.Field(i)
			if !field.IsExported() {
				continue
			}
			if value.Field(i).IsZero() {
				t.Errorf("%s.%s was left at its zero value — the round-trip test cannot "+
					"tell whether the mapper carries it", name, field.Name)
			}
		}
	}

	check("model.User", reflect.ValueOf(filled[model.User](t)).Elem())
	check("model.Session", reflect.ValueOf(filled[model.Session](t)).Elem())
	check("model.Chat", reflect.ValueOf(filled[model.Chat](t)).Elem())
	check("model.ChatMember", reflect.ValueOf(filled[model.ChatMember](t)).Elem())
	check("model.Message", reflect.ValueOf(filled[model.Message](t)).Elem())
	check("model.Attachment", reflect.ValueOf(filled[model.Attachment](t)).Elem())
	check("model.ForwardOrigin", reflect.ValueOf(filled[model.ForwardOrigin](t)).Elem())

	// The nested pointers matter most: an unpopulated Attachment would make
	// TestMessageRoundTrip compare nil against nil and prove nothing.
	msg := filled[model.Message](t)
	if msg.Attachment == nil || msg.Forward == nil {
		t.Fatal("nested pointers were not populated")
	}
	check("model.Message.Attachment", reflect.ValueOf(msg.Attachment).Elem())
	check("model.Message.Forward", reflect.ValueOf(msg.Forward).Elem())
}
