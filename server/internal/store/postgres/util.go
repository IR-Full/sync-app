package postgres

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"time"

	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/golang-migrate/migrate/v4"
	migratepg "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
)

func nowMs() int64 { return time.Now().UnixMilli() }

func directKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

// pairKey namespaces the canonical-pair index by chat type.
//
// The direct form is left UNPREFIXED so every row written before secret chats
// existed still resolves — a prefix on the default would orphan every 1:1 chat in
// the database — a migration nobody asked for, to express a distinction the
// direct chat does not need.
func pairKey(typ model.ChatType, a, b string) string {
	if typ == model.ChatDirect || typ == "" {
		return directKey(a, b)
	}
	return string(typ) + ":" + directKey(a, b)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505" // unique_violation
	}
	return false
}

// migrationFiles are the versioned schema migrations, applied by golang-migrate.
// Each version has an up/down pair; the applied version is tracked in the
// schema_migrations table, so upgrades are ordered and idempotent (and
// rollbackable), replacing the earlier "run one big idempotent script" approach.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrate brings the schema up to the latest version. It opens a short-lived
// database/sql connection (golang-migrate's driver model) separate from the
// runtime pgx pool.
func (s *Store) Migrate(ctx context.Context) error {
	src, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	db, err := sql.Open("pgx", s.dsn)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	driver, err := migratepg.WithInstance(db, &migratepg.Config{})
	if err != nil {
		return err
	}
	m, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
	if err != nil {
		return err
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}
