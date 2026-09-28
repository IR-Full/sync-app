package memory

import (
	"sync"

	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
)

// Store implements all store interfaces over in-memory maps.
type Store struct {
	mu sync.RWMutex

	users       map[string]*model.User
	usersByName map[string]string // username -> id
	devices     map[string]*model.Device
	sessions    map[string]*model.Session
	tokenIndex  map[string]string // token -> session id
	resumeIndex map[string]string // resume token -> session id
	chats       map[string]*model.Chat
	directIndex map[string]string                            // "a|b" (sorted) -> chat id
	members     map[string]map[string]*model.ChatMember      // chatID -> userID -> member
	messages    map[string][]*model.Message                  // chatID -> ordered by Seq
	dedup       map[string]string                            // sender|dedupKey -> messageID
	reads       map[string]map[string]*model.ReadState       // chatID -> userID -> read
	reactions   map[string]map[string]*model.Reaction        // messageID -> userID -> reaction
	calls       map[string]*model.Call                       // callID -> call
	callParts   map[string]map[string]*model.CallParticipant // callID -> userID -> participant
	polls       map[string]*model.Poll                       // pollID -> poll
	pollByMsg   map[string]string                            // messageID -> pollID
	pollVotes   map[string]map[string]map[int32]bool         // pollID -> userID -> option set
	contacts    map[string]map[string]*model.Contact         // ownerID -> userID -> contact
	scheduled   map[string]*model.ScheduledMessage           // id -> pending send
	pins        map[string]map[string]*model.PinnedMessage   // chatID -> msgID -> pin
	drafts      map[string]map[string]*model.Draft           // userID -> chatID -> draft
	invites     map[string]*model.InviteLink                 // code -> invite link
	outbox      []store.OutboxRecord                         // staged events (fifo)
	outboxSent  map[string]bool                              // marked-sent record ids
	// secretQ is the undelivered E2E ciphertext queue, ordered oldest-first per
	// device. A slice rather than a map because every read is "the next N for
	// this device in order" and every write is an append — the only random
	// access is the ack, which is rare and bounded by one page.
	secretQ map[string][]*model.SecretEnvelope // "user|device" -> fifo
	// prevResumeIndex maps a CONSUMED resume token to its session, which is how
	// replay of a rotated token is detected rather than merely rejected.
	prevResumeIndex map[string]string
	twoFactor       map[string]*model.TwoFactor // userID -> enrolment

	subscriptions map[string]*model.Subscription // userID -> subscription
	payments      map[string]*model.Payment      // paymentID -> payment
	// Two indexes over payments, and both are load-bearing rather than
	// conveniences: paymentIdem is what makes a retried checkout resolve to the
	// existing charge instead of a second one, and paymentRefs is how a provider
	// callback finds what it is talking about.
	paymentIdem map[string]string // "user|idempotencyKey" -> paymentID
	paymentRefs map[string]string // "provider|providerRef" -> paymentID
}
