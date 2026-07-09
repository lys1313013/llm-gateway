package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/lys1313013/llm-gateway/backend/internal/models"
)

// ErrNotFound is returned when a single-row lookup yields no result.
var ErrNotFound = errors.New("not found")

// ---------------------------------------------------------------------------
// Provider
// ---------------------------------------------------------------------------

const providerSelectCols = `id, name, openai_base_url, anthropic_base_url, responses_base_url, api_key, remark, quota_url, quota_format, create_time, update_time`

func GetProviders(ctx context.Context) ([]models.Provider, error) {
	rows, err := mustHavePool().Query(ctx,
		`SELECT `+providerSelectCols+` FROM provider ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProviders(rows)
}

func GetProvider(ctx context.Context, id int) (*models.Provider, error) {
	row := mustHavePool().QueryRow(ctx,
		`SELECT `+providerSelectCols+` FROM provider WHERE id = $1`, id)
	p, err := scanProvider(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return p, nil
}

type CreateProviderInput struct {
	Name             string  `json:"name"`
	OpenAIBaseURL    *string `json:"openai_base_url,omitempty"`
	AnthropicBaseURL *string `json:"anthropic_base_url,omitempty"`
	ResponsesBaseURL *string `json:"responses_base_url,omitempty"`
	APIKey           *string `json:"api_key,omitempty"`
	Remark           *string `json:"remark,omitempty"`
	QuotaURL         *string `json:"quota_url,omitempty"`
	QuotaFormat      *string `json:"quota_format,omitempty"`
}

func CreateProvider(ctx context.Context, in CreateProviderInput) (*models.Provider, error) {
	row := mustHavePool().QueryRow(ctx, `
		INSERT INTO provider (name, openai_base_url, anthropic_base_url, responses_base_url, api_key, remark, quota_url, quota_format)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING `+providerSelectCols,
		in.Name, in.OpenAIBaseURL, in.AnthropicBaseURL, in.ResponsesBaseURL, in.APIKey, in.Remark, in.QuotaURL, in.QuotaFormat)
	return scanProvider(row)
}

type UpdateProviderInput struct {
	Name             string  `json:"name"`
	OpenAIBaseURL    *string `json:"openai_base_url,omitempty"`
	AnthropicBaseURL *string `json:"anthropic_base_url,omitempty"`
	ResponsesBaseURL *string `json:"responses_base_url,omitempty"`
	APIKey           *string `json:"api_key,omitempty"`
	Remark           *string `json:"remark,omitempty"`
	QuotaURL         *string `json:"quota_url,omitempty"`
	QuotaFormat      *string `json:"quota_format,omitempty"`
}

func UpdateProvider(ctx context.Context, id int, in UpdateProviderInput) (*models.Provider, error) {
	row := mustHavePool().QueryRow(ctx, `
		UPDATE provider
		   SET name = $2,
		       openai_base_url = $3,
		       anthropic_base_url = $4,
		       responses_base_url = $5,
		       api_key = $6,
		       remark = $7,
		       quota_url = $8,
		       quota_format = $9,
		       update_time = CURRENT_TIMESTAMP
		 WHERE id = $1
		RETURNING `+providerSelectCols,
		id, in.Name, in.OpenAIBaseURL, in.AnthropicBaseURL, in.ResponsesBaseURL, in.APIKey, in.Remark, in.QuotaURL, in.QuotaFormat)
	p, err := scanProvider(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return p, nil
}

func DeleteProvider(ctx context.Context, id int) error {
	tag, err := mustHavePool().Exec(ctx, `DELETE FROM provider WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------------------------------------------------------------------------
// Row scanners
// ---------------------------------------------------------------------------

type rowScanner interface {
	Scan(dest ...any) error
}

func scanProvider(row rowScanner) (*models.Provider, error) {
	var p models.Provider
	err := row.Scan(&p.ID, &p.Name, &p.OpenAIBaseURL, &p.AnthropicBaseURL, &p.ResponsesBaseURL,
		&p.APIKey, &p.Remark, &p.QuotaURL, &p.QuotaFormat, &p.CreateTime, &p.UpdateTime)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan provider: %w", err)
	}
	return &p, nil
}

func scanProviders(rows interface {
	Next() bool
	Err() error
	Scan(dest ...any) error
	Close()
}) ([]models.Provider, error) {
	var out []models.Provider
	for rows.Next() {
		var p models.Provider
		if err := rows.Scan(&p.ID, &p.Name, &p.OpenAIBaseURL, &p.AnthropicBaseURL, &p.ResponsesBaseURL,
			&p.APIKey, &p.Remark, &p.QuotaURL, &p.QuotaFormat, &p.CreateTime, &p.UpdateTime); err != nil {
			return nil, fmt.Errorf("scan providers: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
