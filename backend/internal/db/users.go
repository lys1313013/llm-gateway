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

const userSelectCols = `id, username, password_hash, is_active, created_at, updated_at`

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
		`SELECT `+userSelectCols+` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanUsers(rows)
}

func GetUserCount(ctx context.Context) (int, error) {
	var n int
	err := mustHavePool().QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func CreateUser(ctx context.Context, username, passwordHash string) (*models.User, error) {
	row := mustHavePool().QueryRow(ctx, `
		INSERT INTO users (username, password_hash)
		VALUES ($1, $2)
		RETURNING `+userSelectCols, username, passwordHash)
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

// ---------------------------------------------------------------------------
// Scanners
// ---------------------------------------------------------------------------

func scanUser(row rowScanner) (*models.User, error) {
	var u models.User
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.IsActive, &u.CreatedAt, &u.UpdatedAt)
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
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.IsActive,
			&u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan users: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
