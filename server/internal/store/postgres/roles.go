package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/SyncApp-chat/SyncApp/internal/model"
	"github.com/SyncApp-chat/SyncApp/internal/store"
)

func (s *Store) PlatformRole(ctx context.Context, userID string) (model.PlatformRole, error) {
	var role string
	err := s.pool.QueryRow(ctx, `SELECT role FROM platform_roles WHERE user_id=$1`, atoi(userID)).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", wrap(err)
	}
	return model.PlatformRole(role), nil
}

func (s *Store) ListPlatformRoles(ctx context.Context) ([]model.PlatformRoleGrant, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT user_id, role, granted_by, granted_at FROM platform_roles ORDER BY user_id`)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	var out []model.PlatformRoleGrant
	for rows.Next() {
		var (
			uid  int64
			role string
			g    model.PlatformRoleGrant
		)
		if err := rows.Scan(&uid, &role, &g.GrantedBy, &g.GrantedAt); err != nil {
			return nil, err
		}
		g.UserID, g.Role = itoa(uid), model.PlatformRole(role)
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) SetPlatformRole(ctx context.Context, g model.PlatformRoleGrant) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO platform_roles (user_id, role, granted_by, granted_at) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (user_id) DO UPDATE SET role = EXCLUDED.role,
		     granted_by = EXCLUDED.granted_by, granted_at = EXCLUDED.granted_at`,
		atoi(g.UserID), string(g.Role), g.GrantedBy, g.GrantedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" { // foreign_key_violation: no such user
		return store.ErrNotFound
	}
	return wrap(err)
}

func (s *Store) RemovePlatformRole(ctx context.Context, userID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM platform_roles WHERE user_id=$1`, atoi(userID))
	return wrap(err)
}
