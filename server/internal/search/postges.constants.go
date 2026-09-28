package search

import "github.com/SyncApp-chat/SyncApp/internal/envcfg"

/*
The text search dictionary.

It was hardcoded to 'simple', which does no stemming at all: 'simple' splits on
non-word characters, lowercases, and stops. For an English-or-Russian product
that means "сообщение" and "сообщения" are unrelated tokens, and so are "run" and
"running" — a search finds the exact word the person typed and nothing else,
which users read as the search being broken.

It is configurable rather than fixed because the right dictionary depends on the
audience, and getting it wrong in the other direction is also bad: a Russian
dictionary applied to English text stems by the wrong rules. SYNCAPP_FTS_CONFIG
names any dictionary the server has installed ('russian', 'english', 'simple').

The default stays 'simple' deliberately. Changing the dictionary changes the
stored tsvector, so an existing deployment has to REINDEX before its old rows
match under the new rules — a default that silently degraded every existing
index on upgrade would be worse than one that needs a decision.
*/
var tsConfig = sanitizeFTSConfig(envcfg.GetDefault("SYNCAPP_FTS_CONFIG", "simple"))

// sanitizeFTSConfig bounds the dictionary name to an identifier.
//
// The name is interpolated into SQL (see below for why it cannot be a bound
// parameter), and "it comes from our own environment" is a reason to expect it to
// be well-formed, not a reason to skip checking. An operator typo should fail
// closed on a known-good default rather than produce a query whose shape depends
// on a config string.
func sanitizeFTSConfig(name string) string {
	if name == "" {
		return "simple"
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		ok := c == '_' || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
		if !ok {
			return "simple"
		}
	}
	return name
}

// tsVector and tsQuery are the indexing and querying halves, built once.
//
// The dictionary name is interpolated rather than bound as a parameter because
// Postgres takes it as a regconfig identifier, not a value — `to_tsvector($1,
// $2)` with a parameter works but forces a cast on every call and defeats the
// expression index. It comes from the server's own environment, never from a
// request, so there is nothing here a client can influence.
var (
	tsVector = "to_tsvector('" + tsConfig + "', $5)"
	// websearch_to_tsquery rather than plainto_tsquery: it understands quoted
	// phrases, OR and negation the way people already type into a search box, and
	// it never errors on punctuation the way to_tsquery does.
	tsQuery = "websearch_to_tsquery('" + tsConfig + "', $1)"
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS search_docs (
    message_id BIGINT PRIMARY KEY,
    chat_id    BIGINT NOT NULL,
    sender_id  BIGINT NOT NULL,
    seq        BIGINT NOT NULL,
    body       TEXT NOT NULL,
    created_at BIGINT NOT NULL DEFAULT 0,
    tsv        tsvector
);
ALTER TABLE search_docs ADD COLUMN IF NOT EXISTS created_at BIGINT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_search_tsv ON search_docs USING GIN (tsv);
-- The scoped query filters by chat and orders by recency, so this is the index
-- that serves it once membership has narrowed the candidate set. The old
-- (chat_id, seq DESC) index ordered by the PER-CHAT sequence, which is not a
-- recency order across chats at all.
CREATE INDEX IF NOT EXISTS idx_search_chat_created ON search_docs(chat_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_search_sender ON search_docs(sender_id);
`
