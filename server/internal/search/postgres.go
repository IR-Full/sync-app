package search

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/SyncApp-chat/SyncApp/internal/envcfg"
)

// postgresBackend is the shared, multi-node search index using Postgres
// full-text search (tsvector + GIN). Every gateway node writes to and reads from
// the same table, so search results are consistent regardless of which node
// indexed a message or serves the query. This is the production default; an
// OpenSearch backend can implement the same Backend interface later for richer
// ranking.
type postgresBackend struct {
	pool *pgxpool.Pool
}

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

// NewPostgresBackend connects, ensures the schema, and returns the backend.
func NewPostgresBackend(ctx context.Context, dsn string) (Backend, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	// Modest pool: search is not the hot path, and this leaves headroom under the
	// server's max_connections alongside the message store's larger pool.
	cfg.MaxConns = 10
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		pool.Close()
		return nil, err
	}
	return &postgresBackend{pool: pool}, nil
}

func atoi(s string) int64 { n, _ := strconv.ParseInt(s, 10, 64); return n }

func (b *postgresBackend) Index(ctx context.Context, d Doc) {
	// Upsert; tsv is derived from the body with the configured text search
	// dictionary (see tsConfig).
	_, _ = b.pool.Exec(ctx,
		`INSERT INTO search_docs (message_id, chat_id, sender_id, seq, body, created_at, tsv)
		 VALUES ($1,$2,$3,$4,$5,$6, `+tsVector+`)
		 ON CONFLICT (message_id) DO UPDATE SET body=EXCLUDED.body, tsv=EXCLUDED.tsv,
		   seq=EXCLUDED.seq, created_at=EXCLUDED.created_at`,
		atoi(d.MessageID), atoi(d.ChatID), atoi(d.SenderID), int64(d.Seq), d.Text, d.CreatedAt)
}

func (b *postgresBackend) Delete(ctx context.Context, messageID string) {
	_, _ = b.pool.Exec(ctx, `DELETE FROM search_docs WHERE message_id=$1`, atoi(messageID))
}

func (b *postgresBackend) Search(ctx context.Context, q Query) ([]Doc, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if q.UserID == "" {
		return nil, nil // unscoped: see Query's doc comment
	}

	/*
	   Membership is a SUBQUERY, not a post-filter, and that is the whole point.

	   This query used to be `WHERE tsv @@ plainto_tsquery(...) ORDER BY seq DESC
	   LIMIT n` with no mention of who was asking: it returned the globally best
	   matches and left the service to drop the ones the caller could not see. On
	   any multi-user system the caller's own matches were not in that global page,
	   so the search answered "nothing found" for messages it held.

	   The join is on chat_members in the same database as search_docs, which is how
	   this backend is wired (one DSN for both). q.ChatIDs carries the same
	   permission for backends that cannot join — see Query.

	   Ranking changed too. `ORDER BY seq DESC` sorted by the PER-CHAT sequence
	   number, so a chat with a million messages outranked every other chat by
	   construction. ts_rank puts the best textual match first and created_at breaks
	   ties by actual recency.
	*/
	args := []any{q.Text, atoi(q.UserID)}
	sql := `SELECT d.message_id, d.chat_id, d.sender_id, d.seq, d.body, d.created_at
		 FROM search_docs d, ` + tsQuery + ` q
		 WHERE d.tsv @@ q
		   AND d.chat_id IN (SELECT chat_id FROM chat_members WHERE user_id = $2)`
	if q.ChatID != "" {
		args = append(args, atoi(q.ChatID))
		sql += ` AND d.chat_id = $3`
	}
	if q.SenderID != "" {
		args = append(args, atoi(q.SenderID))
		sql += ` AND d.sender_id = $` + itoa(len(args))
	}
	args = append(args, limit)
	sql += ` ORDER BY ts_rank(d.tsv, q) DESC, d.created_at DESC, d.message_id DESC
		 LIMIT $` + itoa(len(args))

	rows, err := b.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Doc
	for rows.Next() {
		var (
			mid, cid, sid, seq, created int64
			body                        string
		)
		if err := rows.Scan(&mid, &cid, &sid, &seq, &body, &created); err != nil {
			return nil, err
		}
		out = append(out, Doc{
			MessageID: strconv.FormatInt(mid, 10), ChatID: strconv.FormatInt(cid, 10),
			SenderID: strconv.FormatInt(sid, 10), Seq: uint64(seq), Text: body,
			CreatedAt: created,
		})
	}
	return out, rows.Err()
}

func itoa(n int) string { return strconv.Itoa(n) }
