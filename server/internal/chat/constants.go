package chat

import "time"

// authCacheTTL is how long a chat's authorization view (type + member roles) is
// cached. This removes the 1–2 store queries per message on the hot path
// (CanPost/IsMember), at the cost of up to TTL staleness for a just-changed
// membership. Membership mutations invalidate the entry precisely, so the only
// stale window is a concurrent change on another node — acceptable for authz
// (worst case: a removed member posts for a few seconds).
const authCacheTTL = 5 * time.Second

// The cache is bounded in BOTH directions, because an expired entry that is
// never read again is still resident: without eviction the map keeps one entry
// per chat the node has ever touched, each holding that chat's whole role map.
//
//	authCacheMax   — hard ceiling on cached chats. It also bounds the sweep
//	                 below, which is what keeps the sweep cheap enough to run
//	                 under the write lock.
//	authSweepEvery — how often expired entries are collected. Far longer than the
//	                 TTL: correctness never depends on the sweep (an expired
//	                 entry is reloaded on read), only memory does.
const (
	authCacheMax   = 50_000
	authSweepEvery = 30 * time.Second
)

// maxCachedMembers is the size above which a chat's membership is NOT held as a
// whole.
//
// Caching the entire role map is the right shape for an ordinary chat: one query
// then answers every member's "may I post?" for the TTL, which is the N+1 this
// cache exists to kill. It is the wrong shape for a channel with a million
// members, where the same trick means a million-entry map resident for a handful
// of authorization questions — and every authorization question is about ONE
// user.
//
// So above the threshold the service keeps only the chat's type and answers role
// questions with a primary-key probe, memoized per (chat, user). The bound is
// generous: a chat this big is a broadcast channel, where a member asks an
// authorization question rarely and the few who post are cached individually.
const maxCachedMembers = 2_000

// Chat-list page bounds. The list is assembled per row (chat + role +, for a
// 1:1, the peer), so the page size is what caps the work one request can ask
// for; a client that wants everything walks the cursor.
const (
	defaultChatPage = 100
	maxChatPage     = 200
)
