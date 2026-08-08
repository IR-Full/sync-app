package wire

import (
	"reflect"
	"testing"
)

// TestProtoCodecRoundTrip verifies every body type survives a protobuf
// marshal→unmarshal cycle unchanged. It guards the hand-written conversions in
// protocodec.go against drift when fields are added.
func TestProtoCodecRoundTrip(t *testing.T) {
	cases := []any{
		HelloBody{ClientVersion: "1", DeviceID: "d", Platform: "cli", Caps: CapCompression | CapResume, ResumeToken: "rt"},
		WelcomeBody{ServerVersion: "s", SessionID: "sid", Caps: CapResume, HeartbeatMs: 20000, MaxInflight: 256, ResumeSupported: true},
		AuthBody{Token: "t", Username: "u", Password: "p", Register: true, DisplayName: "Alice"},
		AuthOKBody{UserID: "u", DeviceID: "d", SessionID: "s", Token: "t", ResumeToken: "rt",
			Username: "alice", DisplayName: "Alice", AvatarRef: "m1-abc"},
		SendBody{ChatID: "c", DedupKey: "k", Text: "hi", MediaRef: "m", ReplyTo: "r"},
		SendAckBody{DedupKey: "k", MessageID: "m", ChatID: "c", ChatSeq: 5, Timestamp: 123, Duplicate: true},
		NewMessageBody{MessageID: "m", ChatID: "c", SenderID: "s", ChatSeq: 7, Text: "t", MediaRef: "mr", ReplyTo: "rp", Edited: true, Deleted: false, Timestamp: 99},
		ReadBody{ChatID: "c", UpToMessageID: "m", UpToChatSeq: 3},
		ReadUpdateBody{ChatID: "c", UserID: "u", UpToChatSeq: 4},
		TypingBody{ChatID: "c", UserID: "u", Active: true},
		PresenceBody{UserID: "u", Online: true, LastSeenMs: 555},
		EditBody{ChatID: "c", MessageID: "m", Text: "new"},
		DeleteBody{ChatID: "c", MessageID: "m", ForAll: true},
		HistoryBody{ChatID: "c", BeforeSeq: 10, Limit: 50},
		HistoryOKBody{ChatID: "c", NextBefore: 2, Done: true},
		ResumeBody{ResumeToken: "rt", LastAckSeq: 8},
		ResumeOKBody{SessionID: "s", FromSeq: 8},
		ErrorBody{Code: ErrRateLimited, Message: "slow down", RetryAfterMs: 1000},
		MediaInitBody{Filename: "f.txt", ContentType: "text/plain", Size: 1024},
		MediaTicketBody{MediaRef: "m", UploadURL: "http://u", ExpiresAt: 111},
		MediaFetchBody{MediaRef: "m"},
		MediaURLBody{MediaRef: "m", DownloadURL: "http://d", ExpiresAt: 222},
		SearchBody{Query: "pizza", Limit: 20},
		SearchResultsBody{Query: "pizza", Hits: []SearchHit{{MessageID: "m", ChatID: "c", SenderID: "s", Seq: 1, Text: "pizza time"}}},
		KeyPublishBody{IdentityKey: "ik", SigningKey: "sk", SignedPreKey: "spk", SignedPreKeySig: "sig", PreKeys: []string{"a", "b"}},
		KeyFetchBody{UserID: "u", DeviceID: "d"},
		KeyBundleBody{UserID: "u", DeviceID: "d", IdentityKey: "ik", SigningKey: "sk", SignedPreKey: "spk", SignedPreKeySig: "sig", OneTimePreKey: "otp"},
		KeyBundlesBody{UserID: "u", Bundles: []KeyBundleBody{{UserID: "u", DeviceID: "d1", IdentityKey: "ik"}, {UserID: "u", DeviceID: "d2", IdentityKey: "ik2"}}},
		SecretMsgBody{ToUserID: "u2", ToDeviceID: "d2", FromUserID: "u1", FromDeviceID: "d1", RatchetHeader: "hdr", Ciphertext: "ct"},
		ChatListBody{After: "12", Limit: 50},
		ChatsBody{Chats: []ChatSummary{
			{ChatID: "1", Type: "direct", LastSeq: 9, MyRole: "member", PeerID: "u2"},
			{ChatID: "2", Type: "group", Title: "T", OwnerID: "o", Username: "handle", LastSeq: 3, MyRole: "owner"},
		}, NextAfter: "2", Done: false},
		ProfileGetBody{Target: "@alice"},
		ProfileSetBody{DisplayName: "Alice", AvatarRef: "m1-abc", ClearAvatar: true},
		ProfileBody{UserID: "u", Username: "alice", DisplayName: "Alice", AvatarRef: "m1-abc"},
		ChatExportBody{ChatID: "c"},
		ChatExportResultBody{ChatID: "c", Type: "group", Title: "T", OwnerID: "o",
			Members:  []ChatMemberInfo{{UserID: "u", Role: "owner", JoinedAt: 1}},
			Messages: []NewMessageBody{{MessageID: "m", ChatID: "c", SenderID: "s", ChatSeq: 1, Text: "x", Timestamp: 2}}},
	}

	for _, in := range cases {
		b := Marshal(in)
		// Decode into a fresh pointer of the same type.
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

// TestProtoCodecIsBinary confirms bodies are protobuf, not JSON (no field names
// on the wire).
func TestProtoCodecIsBinary(t *testing.T) {
	b := Marshal(SendBody{ChatID: "c", Text: "hello"})
	if len(b) == 0 {
		t.Fatal("empty encoding")
	}
	// A JSON encoding would contain the literal key "chat_id"; protobuf must not.
	if containsSub(b, []byte("chat_id")) {
		t.Fatal("body appears to be JSON, expected protobuf")
	}
}

func containsSub(haystack, needle []byte) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == string(needle) {
			return true
		}
	}
	return false
}
