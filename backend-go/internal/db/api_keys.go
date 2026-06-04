package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lys1313013/llm-gateway/backend-go/internal/models"
)

// ---------------------------------------------------------------------------
// API keys
// ---------------------------------------------------------------------------

const apiKeySelectCols = `id, user_id, key_hash, key_prefix, key_value, name, is_active, created_at, last_used_at`

func GetAPIKeyByHash(ctx context.Context, hash string) (*models.APIKey, error) {
	row := mustHavePool().QueryRow(ctx, `
		SELECT `+apiKeySelectCols+` FROM api_keys WHERE key_hash = $1`, hash)
	k, err := scanAPIKey(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return k, nil
}

func GetAPIKeysByUser(ctx context.Context, userID int) ([]models.APIKey, error) {
	rows, err := mustHavePool().Query(ctx, `
		SELECT `+apiKeySelectCols+`
		FROM api_keys WHERE user_id = $1 ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAPIKeys(rows)
}

type CreateAPIKeyInput struct {
	UserID    int
	KeyHash   string
	KeyPrefix string
	KeyValue  *string
	Name      string
}

func CreateAPIKey(ctx context.Context, in CreateAPIKeyInput) (*models.APIKey, error) {
	row := mustHavePool().QueryRow(ctx, `
		INSERT INTO api_keys (user_id, key_hash, key_prefix, key_value, name)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+apiKeySelectCols,
		in.UserID, in.KeyHash, in.KeyPrefix, in.KeyValue, in.Name)
	return scanAPIKey(row)
}

func DeleteAPIKey(ctx context.Context, id int) error {
	tag, err := mustHavePool().Exec(ctx, `DELETE FROM api_keys WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func ToggleAPIKey(ctx context.Context, id int, isActive bool) (*models.APIKey, error) {
	row := mustHavePool().QueryRow(ctx, `
		UPDATE api_keys SET is_active = $2 WHERE id = $1
		RETURNING `+apiKeySelectCols, id, isActive)
	k, err := scanAPIKey(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return k, nil
}

func UpdateAPIKeyName(ctx context.Context, id int, name string) (*models.APIKey, error) {
	row := mustHavePool().QueryRow(ctx, `
		UPDATE api_keys SET name = $2 WHERE id = $1
		RETURNING `+apiKeySelectCols, id, name)
	k, err := scanAPIKey(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return k, nil
}

func UpdateAPIKeyLastUsed(ctx context.Context, id int) error {
	_, err := mustHavePool().Exec(ctx,
		`UPDATE api_keys SET last_used_at = $2 WHERE id = $1`,
		id, time.Now())
	return err
}

// ---------------------------------------------------------------------------
// Scanners
// ---------------------------------------------------------------------------

func scanAPIKey(row rowScanner) (*models.APIKey, error) {
	var k models.APIKey
	err := row.Scan(&k.ID, &k.UserID, &k.KeyHash, &k.KeyPrefix, &k.KeyValue,
		&k.Name, &k.IsActive, &k.CreatedAt, &k.LastUsedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan api key: %w", err)
	}
	return &k, nil
}

func scanAPIKeys(rows interface {
	Next() bool
	Err() error
	Scan(dest ...any) error
	Close()
}) ([]models.APIKey, error) {
	var out []models.APIKey
	for rows.Next() {
		var k models.APIKey
		if err := rows.Scan(&k.ID, &k.UserID, &k.KeyHash, &k.KeyPrefix, &k.KeyValue,
			&k.Name, &k.IsActive, &k.CreatedAt, &k.LastUsedAt); err != nil {
			return nil, fmt.Errorf("scan api keys: %w", err)
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
