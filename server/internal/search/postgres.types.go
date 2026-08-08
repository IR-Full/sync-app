package search

import "github.com/jackc/pgx/v5/pgxpool"

// postgresBackend is the shared, multi-node search index using Postgres
// full-text search (tsvector + GIN). Every gateway node writes to and reads from
// the same table, so search results are consistent regardless of which node
// indexed a message or serves the query. This is the production default; an
// OpenSearch backend can implement the same Backend interface later for richer
// ranking.
type postgresBackend struct {
	pool *pgxpool.Pool
}
