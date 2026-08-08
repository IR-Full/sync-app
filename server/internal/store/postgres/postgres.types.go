package postgres

import "github.com/jackc/pgx/v5/pgxpool"

// Store implements every store interface over one pgx pool.
type Store struct {
	pool *pgxpool.Pool
	// readPool is an optional connection to a read replica. Lag-tolerant read
	// paths (message history, read receipts) are routed here to offload the
	// primary; authorization/write-path reads always use the primary. nil = no
	// replica, everything reads from the primary.
	readPool *pgxpool.Pool
	dsn      string // kept for the migration runner (opens its own database/sql conn)
	batcher  *batcher
}

// rowScanner is satisfied by both pgx.Row (QueryRow) and pgx.Rows (Query loop).
type rowScanner interface{ Scan(dest ...any) error }
