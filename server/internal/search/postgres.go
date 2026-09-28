package search

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
)

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
