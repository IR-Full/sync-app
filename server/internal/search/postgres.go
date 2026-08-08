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
	// Upsert; tsv is derived from the body with the 'simple' config (language-
	// agnostic; swap to a language config or add unaccent in production).
	_, _ = b.pool.Exec(ctx,
		`INSERT INTO search_docs (message_id, chat_id, sender_id, seq, body, tsv)
		 VALUES ($1,$2,$3,$4,$5, to_tsvector('simple', $5))
		 ON CONFLICT (message_id) DO UPDATE SET body=EXCLUDED.body, tsv=EXCLUDED.tsv, seq=EXCLUDED.seq`,
		atoi(d.MessageID), atoi(d.ChatID), atoi(d.SenderID), int64(d.Seq), d.Text)
}

func (b *postgresBackend) Delete(ctx context.Context, messageID string) {
	_, _ = b.pool.Exec(ctx, `DELETE FROM search_docs WHERE message_id=$1`, atoi(messageID))
}

func (b *postgresBackend) Search(ctx context.Context, query string, limit int) ([]Doc, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := b.pool.Query(ctx,
		`SELECT message_id, chat_id, sender_id, seq, body
		 FROM search_docs
		 WHERE tsv @@ plainto_tsquery('simple', $1)
		 ORDER BY seq DESC LIMIT $2`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Doc
	for rows.Next() {
		var (
			mid, cid, sid, seq int64
			body               string
		)
		if err := rows.Scan(&mid, &cid, &sid, &seq, &body); err != nil {
			return nil, err
		}
		out = append(out, Doc{
			MessageID: strconv.FormatInt(mid, 10), ChatID: strconv.FormatInt(cid, 10),
			SenderID: strconv.FormatInt(sid, 10), Seq: uint64(seq), Text: body,
		})
	}
	return out, rows.Err()
}
