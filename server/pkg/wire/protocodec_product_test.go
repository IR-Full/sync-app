package wire

import (
	"reflect"
	"testing"
)

// Round-trip coverage for the PRODUCT-surface bodies — reactions, threads,
// calls, polls, contacts, forwarding, scheduling, pins, drafts, handles,
// invites, roles, push tokens, chat creation and account deletion.
//
// TestProtoCodecRoundTrip covers the messaging core; these were added later and
// never joined it, so ~20 hand-written conversions in protocodec.go had no test
// at all. They are exactly the code where a field silently goes missing: the
// struct still compiles, the wire still parses, and the field simply arrives
// zero at the far end.
func TestProtoCodecProductRoundTrip(t *testing.T) {
	cases := []any{
		// --- reactions & threads
		ReactBody{ChatID: "c1", MessageID: "m1", Emoji: "👍"},
		ReactUpdateBody{
			ChatID: "c1", MessageID: "m1", UserID: "u1", Emoji: "👍", Added: true,
			Counts: map[string]int{"👍": 3, "🎉": 1},
		},
		ThreadBody{ChatID: "c1", RootID: "m1", AfterSeq: 5, Limit: 50},
		ThreadOKBody{ChatID: "c1", RootID: "m1", NextAfter: 9, Done: true},

		// --- calls
		CallInviteBody{ChatID: "c1", Kind: "video"},
		CallActionBody{CallID: "call1"},
		CallStateBody{
			CallID: "call1", ChatID: "c1", InitiatorID: "u1", Kind: "audio", State: "ringing",
			Participants: []CallParticipant{
				{UserID: "u1", DeviceID: "d1", State: "joined"},
				{UserID: "u2", State: "invited"},
			},
		},
		CallSignalBody{
			CallID: "call1", ToUserID: "u2", ToDeviceID: "d2",
			FromUserID: "u1", FromDeviceID: "d1",
			SignalType: "offer", Payload: "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\n",
		},

		// --- polls
		PollCreateBody{ChatID: "c1", Question: "Tabs or spaces?", Options: []string{"Tabs", "Spaces"}, MultiChoice: true, Anonymous: true},
		PollVoteBody{PollID: "p1", Option: 1},
		PollCloseBody{PollID: "p1"},
		PollStateBody{
			PollID: "p1", ChatID: "c1", MessageID: "m1", Question: "Q",
			Options:    []PollOption{{Index: 0, Text: "Tabs", Votes: 2}, {Index: 1, Text: "Spaces", Votes: 5}},
			TotalVotes: 7, MultiChoice: true, Anonymous: true, Closed: true, MyVotes: []int32{0, 1},
		},

		// --- contacts & blocking
		ContactAddBody{Target: "@bob", Name: "Bob"},
		ContactRemoveBody{Target: "u2"},
		ContactSyncBody{Since: 1700000000000},
		ContactListBody{
			Contacts: []Contact{
				{UserID: "u2", Name: "Bob", UpdatedAt: 111},
				{UserID: "u3", Blocked: true, UpdatedAt: 222},
			},
			Cursor: 222,
		},
		BlockBody{Target: "u3", Blocked: true},

		// --- forwarding
		ForwardBody{FromChatID: "c1", MessageID: "m1", ToChatID: "c2", DedupKey: "k1"},

		// --- scheduling
		ScheduleBody{
			ChatID: "c1", Text: "later", MediaRef: "m1", ReplyTo: "r1",
			TTLSeconds: 30, SendAt: 1700000000000,
			Attachment: &Attachment{
				Kind: "voice", MediaRef: "m2", Filename: "note.ogg", MIME: "audio/ogg",
				Size: 2048, DurationMs: 5000, Waveform: []int32{1, 2, 3}, ThumbRef: "t1",
			},
		},
		ScheduleListBody{ChatID: "c1"},
		ScheduleCancelBody{ID: "s1"},
		ScheduledBody{Items: []ScheduledItem{
			{ID: "s1", ChatID: "c1", Text: "later", SendAt: 111},
			{ID: "s2", ChatID: "c1", SendAt: 222},
		}},

		// --- pins & drafts
		PinBody{ChatID: "c1", MessageID: "m1"},
		PinnedBody{ChatID: "c1", Pins: []Pin{
			{MessageID: "m1", PinnedBy: "u1", PinnedAt: 111},
			{MessageID: "m2", PinnedBy: "u2", PinnedAt: 222},
		}},
		DraftBody{ChatID: "c1", Text: "unsent", ReplyTo: "r1"},
		DraftSyncBody{Since: 1700000000000},
		DraftsBody{Drafts: []DraftItem{
			{ChatID: "c1", Text: "unsent", ReplyTo: "r1", UpdatedAt: 111},
			{ChatID: "c2", UpdatedAt: 222},
		}, Cursor: 222},

		// --- handles, invites, roles
		SetUsernameBody{ChatID: "c1", Username: "news"},
		InviteCreateBody{ChatID: "c1", ExpiresAt: 1700000000000, MaxUses: 5},
		InviteRevokeBody{ChatID: "c1", Code: "abc"},
		InviteListBody{ChatID: "c1"},
		InvitesBody{
			Links:      []InviteLink{{Code: "abc", ChatID: "c1", ExpiresAt: 111, MaxUses: 5, Uses: 2}},
			JoinedChat: "c1",
		},
		JoinBody{Code: "abc"},
		JoinBody{Handle: "@news"},
		SetRoleBody{ChatID: "c1", UserID: "u2", Role: "admin"},

		// --- chats, push, account
		ChatCreateBody{Type: "group", Title: "Team", Members: []string{"u2", "u3"}},
		ChatInfoBody{ChatID: "c1", Type: "group", Title: "Team", OwnerID: "u1"},
		PushTokenBody{Token: "apns-token"},
		AccountDeleteBody{Password: "secret123", Reason: "leaving"},
		AccountDeletedBody{UserID: "u1", DeletedAt: 1700000000000},

		// --- internal fanout envelope
		FanoutShardBody{
			Body:    NewMessageBody{MessageID: "m1", ChatID: "c1", SenderID: "u1", ChatSeq: 3, Text: "x", Timestamp: 9},
			Members: []string{"u2", "u3", "u4"},
		},
	}

	for _, in := range cases {
		b := Marshal(in)
		out := reflect.New(reflect.TypeOf(in)).Interface()
		if err := Unmarshal(b, out); err != nil {
			t.Fatalf("%T: unmarshal: %v", in, err)
		}
		got := reflect.ValueOf(out).Elem().Interface()
		if !reflect.DeepEqual(in, got) {
			t.Fatalf("%T round-trip mismatch:\n in=%+v\nout=%+v", in, in, got)
		}
	}
}

// An attachment is the richest nested body, and it rides three different
// messages. Its optional fields are where a dropped conversion hides.
func TestAttachmentSurvivesOnEveryCarrier(t *testing.T) {
	att := &Attachment{
		Kind: "video_note", MediaRef: "m1", Filename: "clip.mp4", MIME: "video/mp4",
		Size: 4096, DurationMs: 8000, Waveform: []int32{5, 6, 7}, Width: 640, Height: 480, ThumbRef: "t1",
	}

	t.Run("SendBody", func(t *testing.T) {
		in := SendBody{ChatID: "c1", DedupKey: "k", Attachment: att}
		var out SendBody
		if err := Unmarshal(Marshal(in), &out); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(in.Attachment, out.Attachment) {
			t.Fatalf("attachment lost:\n in=%+v\nout=%+v", in.Attachment, out.Attachment)
		}
	})

	t.Run("NewMessageBody", func(t *testing.T) {
		in := NewMessageBody{MessageID: "m", ChatID: "c", SenderID: "s", ChatSeq: 1, Attachment: att}
		var out NewMessageBody
		if err := Unmarshal(Marshal(in), &out); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(in.Attachment, out.Attachment) {
			t.Fatalf("attachment lost:\n in=%+v\nout=%+v", in.Attachment, out.Attachment)
		}
	})

	t.Run("ScheduleBody", func(t *testing.T) {
		in := ScheduleBody{ChatID: "c1", SendAt: 1, Attachment: att}
		var out ScheduleBody
		if err := Unmarshal(Marshal(in), &out); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(in.Attachment, out.Attachment) {
			t.Fatalf("attachment lost:\n in=%+v\nout=%+v", in.Attachment, out.Attachment)
		}
	})
}

// Forward provenance and the self-destruct deadline are the two fields a client
// can only render if the SERVER puts them on the wire, so they get their own
// assertion rather than riding a struct comparison.
func TestForwardProvenanceAndExpirySurvive(t *testing.T) {
	in := NewMessageBody{
		MessageID: "m1", ChatID: "c2", SenderID: "u1", ChatSeq: 4, Text: "forwarded",
		Timestamp: 9, ExpiresAt: 1700000000000,
		Forward: &ForwardOrigin{ChatID: "c1", MessageID: "m0", SenderID: "u9"},
	}
	var out NewMessageBody
	if err := Unmarshal(Marshal(in), &out); err != nil {
		t.Fatal(err)
	}
	if out.Forward == nil {
		t.Fatal("forward provenance dropped — the client would show an origin-less forward")
	}
	if !reflect.DeepEqual(in.Forward, out.Forward) {
		t.Fatalf("forward origin changed: %+v", out.Forward)
	}
	if out.ExpiresAt != in.ExpiresAt {
		t.Fatalf("self-destruct deadline lost: %d", out.ExpiresAt)
	}
}

// proto3 encodes a zero value as nothing at all, so every body has to survive
// being entirely empty — this is the case that turns "absent" into a decode
// error if a conversion is careless.
func TestEmptyBodiesRoundTrip(t *testing.T) {
	cases := []any{
		ReactBody{}, ReactUpdateBody{}, ThreadBody{}, ThreadOKBody{},
		CallInviteBody{}, CallActionBody{}, CallStateBody{}, CallSignalBody{},
		PollCreateBody{}, PollVoteBody{}, PollCloseBody{}, PollStateBody{},
		ContactAddBody{}, ContactRemoveBody{}, ContactSyncBody{}, ContactListBody{}, BlockBody{},
		ForwardBody{}, ScheduleBody{}, ScheduleListBody{}, ScheduleCancelBody{}, ScheduledBody{},
		PinBody{}, PinnedBody{}, DraftBody{}, DraftSyncBody{}, DraftsBody{},
		SetUsernameBody{}, InviteCreateBody{}, InviteRevokeBody{}, InviteListBody{}, InvitesBody{},
		JoinBody{}, SetRoleBody{}, ChatCreateBody{}, ChatInfoBody{}, PushTokenBody{},
		AccountDeleteBody{}, AccountDeletedBody{}, FanoutShardBody{},
	}
	for _, in := range cases {
		b := Marshal(in)
		out := reflect.New(reflect.TypeOf(in)).Interface()
		if err := Unmarshal(b, out); err != nil {
			t.Fatalf("%T: empty body failed to decode: %v", in, err)
		}
		got := reflect.ValueOf(out).Elem().Interface()
		if !reflect.DeepEqual(in, got) {
			t.Fatalf("%T empty round-trip mismatch:\n in=%+v\nout=%+v", in, in, got)
		}
	}
}

// A tally is a map, which protobuf has no native ordering for — so it has to
// come back with the same contents regardless of iteration order.
func TestReactionCountsSurviveAsAMap(t *testing.T) {
	in := ReactUpdateBody{
		ChatID: "c", MessageID: "m", UserID: "u", Emoji: "👍", Added: true,
		Counts: map[string]int{"👍": 10, "🎉": 4, "❤️": 1, "😀": 7},
	}
	var out ReactUpdateBody
	if err := Unmarshal(Marshal(in), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Counts) != len(in.Counts) {
		t.Fatalf("tally lost entries: %v", out.Counts)
	}
	for emoji, n := range in.Counts {
		if out.Counts[emoji] != n {
			t.Fatalf("count for %s = %d, want %d", emoji, out.Counts[emoji], n)
		}
	}
}
