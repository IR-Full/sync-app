package wire

var sharedDict = []byte(
	// Common protobuf/JSON field names and chat tokens that recur across bodies.
	"message_id chat_id sender_id user_id device_id session_id dedup_key media_ref " +
		"reply_to chat_seq timestamp up_to_chat_seq resume_token identity_key signed_prekey " +
		"one_time_prekey ciphertext ratchet_header from_user_id to_user_id online last_seen_ms " +
		"typing presence read edited deleted duplicate content_type upload_url download_url " +
		"hello welcome auth ok error ping pong send new history search notify " +
		"the be to of and a in that have I it for not on with he as you do at this but his by from " +
		"hi hey hello thanks ok okay yes no lol please sorry good morning night see you later ",
)

// dictID identifies the raw content dictionary; both ends must agree on it.
const dictID = 1
