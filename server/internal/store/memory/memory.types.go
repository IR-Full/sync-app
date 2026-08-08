package memory

import (
	"sync"

	"github.com/synapse-chat/synapse/internal/model"
	"github.com/synapse-chat/synapse/internal/store"
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
}
