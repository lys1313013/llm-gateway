package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/lys1313013/llm-gateway/backend/internal/models"
)

// ---------------------------------------------------------------------------
// Users
// ---------------------------------------------------------------------------

const userSelectCols = `id, username, password_hash, is_active, role, team_id, created_at, updated_at`

const userWithTeamCols = `u.id, u.username, u.password_hash, u.is_active, u.role, u.team_id, COALESCE(t.name, ''),
	u.created_at, u.updated_at`

func GetUserByUsername(ctx context.Context, username string) (*models.User, error) {
	row := mustHavePool().QueryRow(ctx,
		`SELECT `+userSelectCols+` FROM users WHERE username = $1`, username)
	u, err := scanUser(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return u, nil
}

func GetUserByID(ctx context.Context, id int) (*models.User, error) {
	row := mustHavePool().QueryRow(ctx,
		`SELECT `+userSelectCols+` FROM users WHERE id = $1`, id)
	u, err := scanUser(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return u, nil
}

func GetUsers(ctx context.Context) ([]models.User, error) {
	rows, err := mustHavePool().Query(ctx,
		`SELECT `+userWithTeamCols+` FROM users u
		 LEFT JOIN team t ON t.id = u.team_id
		 ORDER BY u.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanUsersWithTeam(rows)
}

func GetUserCount(ctx context.Context) (int, error) {
	var n int
	err := mustHavePool().QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func CreateUser(ctx context.Context, username, passwordHash string, role int) (*models.User, error) {
	row := mustHavePool().QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, $2, $3)
		RETURNING `+userSelectCols, username, passwordHash, role)
	return scanUser(row)
}

func UpdateUserPassword(ctx context.Context, id int, passwordHash string) error {
	tag, err := mustHavePool().Exec(ctx,
		`UPDATE users SET password_hash = $2, updated_at = CURRENT_TIMESTAMP WHERE id = $1`,
		id, passwordHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func DeleteUser(ctx context.Context, id int) error {
	tag, err := mustHavePool().Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpgradeLegacyRoles sets all users with no role (0 or NULL) to role=1 (root).
// Used at startup to migrate pre-role deployments.
func UpgradeLegacyRoles(ctx context.Context) error {
	_, err := mustHavePool().Exec(ctx,
		`UPDATE users SET role = 1 WHERE role = 0 OR role IS NULL`)
	return err
}

func UpdateUserRole(ctx context.Context, id, role int) error {
	tag, err := mustHavePool().Exec(ctx,
		`UPDATE users SET role = $2, updated_at = CURRENT_TIMESTAMP WHERE id = $1`, id, role)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func UpdateUserTeam(ctx context.Context, userID int, teamID *int) error {
	tag, err := mustHavePool().Exec(ctx,
		`UPDATE users SET team_id = $2, updated_at = CURRENT_TIMESTAMP WHERE id = $1`,
		userID, teamID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------------------------------------------------------------------------
// Scanners
// ---------------------------------------------------------------------------

func scanUser(row rowScanner) (*models.User, error) {
	var u models.User
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.IsActive, &u.Role,
		&u.TeamID, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan user: %w", err)
	}
	return &u, nil
}

func scanUsers(rows interface {
	Next() bool
	Err() error
	Scan(dest ...any) error
	Close()
}) ([]models.User, error) {
	var out []models.User
	for rows.Next() {
		var u models.User
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.IsActive, &u.Role,
			&u.TeamID, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan users: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func scanUsersWithTeam(rows interface {
	Next() bool
	Err() error
	Scan(dest ...any) error
	Close()
}) ([]models.User, error) {
	var out []models.User
	for rows.Next() {
		var u models.User
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.IsActive, &u.Role,
			&u.TeamID, &u.TeamName,
			&u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan users with team: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
