package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lys1313013/llm-gateway/backend/internal/models"
)

// ---------------------------------------------------------------------------
// Exposed model
// ---------------------------------------------------------------------------

const exposedSelectCols = `id, model_id, owned_by, is_active, team_id, last_openai_test_time, last_anthropic_test_time, create_time, update_time`

const exposedWithTeamCols = `e.id, e.model_id, e.owned_by, e.is_active, e.team_id, COALESCE(t.name, ''),
	e.last_openai_test_time, e.last_anthropic_test_time,
	e.create_time, e.update_time`

func GetExposedModels(ctx context.Context) ([]models.ExposedModel, error) {
	rows, err := mustHavePool().Query(ctx,
		`SELECT `+exposedSelectCols+` FROM exposed_model ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanExposedModels(rows)
}

func GetActiveExposedModels(ctx context.Context) ([]models.ExposedModel, error) {
	rows, err := mustHavePool().Query(ctx,
		`SELECT `+exposedSelectCols+` FROM exposed_model WHERE is_active = TRUE ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanExposedModels(rows)
}

func GetExposedModel(ctx context.Context, id int) (*models.ExposedModel, error) {
	row := mustHavePool().QueryRow(ctx,
		`SELECT `+exposedSelectCols+` FROM exposed_model WHERE id = $1`, id)
	m, err := scanExposedModel(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return m, nil
}

func GetExposedModelByName(ctx context.Context, name string) (*models.ExposedModel, error) {
	row := mustHavePool().QueryRow(ctx,
		`SELECT `+exposedSelectCols+` FROM exposed_model WHERE model_id = $1`, name)
	m, err := scanExposedModel(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return m, nil
}

type CreateExposedModelInput struct {
	ModelID  string  `json:"model_id"`
	OwnedBy  *string `json:"owned_by,omitempty"`
	IsActive *bool   `json:"is_active,omitempty"`
	TeamID   *int    `json:"team_id,omitempty"`
}

func CreateExposedModel(ctx context.Context, in CreateExposedModelInput) (*models.ExposedModel, error) {
	ownedBy := "organization"
	if in.OwnedBy != nil {
		ownedBy = *in.OwnedBy
	}
	active := true
	if in.IsActive != nil {
		active = *in.IsActive
	}
	row := mustHavePool().QueryRow(ctx, `
		INSERT INTO exposed_model (model_id, owned_by, is_active, team_id)
		VALUES ($1, $2, $3, $4)
		RETURNING `+exposedSelectCols,
		in.ModelID, ownedBy, active, in.TeamID)
	return scanExposedModel(row)
}

type UpdateExposedModelInput struct {
	ModelID  string  `json:"model_id"`
	OwnedBy  *string `json:"owned_by,omitempty"`
	IsActive *bool   `json:"is_active,omitempty"`
	TeamID   *int    `json:"team_id,omitempty"`
}

func UpdateExposedModel(ctx context.Context, id int, in UpdateExposedModelInput) (*models.ExposedModel, error) {
	row := mustHavePool().QueryRow(ctx, `
		UPDATE exposed_model
		   SET model_id = $2, owned_by = $3, is_active = $4, team_id = $5, update_time = CURRENT_TIMESTAMP
		 WHERE id = $1
		RETURNING `+exposedSelectCols,
		id, in.ModelID, in.OwnedBy, in.IsActive, in.TeamID)
	m, err := scanExposedModel(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return m, nil
}

func DeleteExposedModel(ctx context.Context, id int) error {
	tag, err := mustHavePool().Exec(ctx, `DELETE FROM exposed_model WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func UpdateExposedModelTestTime(ctx context.Context, id int, protocol string) (*models.ExposedModel, error) {
	col := "last_openai_test_time"
	if protocol == "anthropic" {
		col = "last_anthropic_test_time"
	}
	row := mustHavePool().QueryRow(ctx, fmt.Sprintf(`
		UPDATE exposed_model
		   SET %s = $2, update_time = CURRENT_TIMESTAMP
		 WHERE id = $1
		RETURNING `+exposedSelectCols, col),
		id, time.Now())
	m, err := scanExposedModel(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return m, nil
}

// ---------------------------------------------------------------------------
// Scanners
// ---------------------------------------------------------------------------

func scanExposedModel(row rowScanner) (*models.ExposedModel, error) {
	var m models.ExposedModel
	err := row.Scan(&m.ID, &m.ModelID, &m.OwnedBy, &m.IsActive,
		&m.TeamID,
		&m.LastOpenAITestTime, &m.LastAnthropicTestTime,
		&m.CreateTime, &m.UpdateTime)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan exposed: %w", err)
	}
	return &m, nil
}

func scanExposedModels(rows interface {
	Next() bool
	Err() error
	Scan(dest ...any) error
	Close()
}) ([]models.ExposedModel, error) {
	var out []models.ExposedModel
	for rows.Next() {
		var m models.ExposedModel
		if err := rows.Scan(&m.ID, &m.ModelID, &m.OwnedBy, &m.IsActive,
			&m.TeamID,
			&m.LastOpenAITestTime, &m.LastAnthropicTestTime,
			&m.CreateTime, &m.UpdateTime); err != nil {
			return nil, fmt.Errorf("scan exposed list: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Team-aware queries
// ---------------------------------------------------------------------------

func GetExposedModelsForTeam(ctx context.Context, teamID *int) ([]models.ExposedModel, error) {
	// 未分配团队的用户看不到任何模型
	if teamID == nil {
		return nil, nil
	}
	rows, err := mustHavePool().Query(ctx, `
		SELECT `+exposedWithTeamCols+` FROM exposed_model e
		LEFT JOIN team t ON t.id = e.team_id
		WHERE e.team_id IS NULL OR e.team_id = $1
		ORDER BY e.id`, *teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanExposedModelsWithTeam(rows)
}

func GetActiveExposedModelsForTeam(ctx context.Context, teamID *int) ([]models.ExposedModel, error) {
	// 未分配团队的用户看不到任何模型
	if teamID == nil {
		return nil, nil
	}
	rows, err := mustHavePool().Query(ctx, `
		SELECT `+exposedWithTeamCols+` FROM exposed_model e
		LEFT JOIN team t ON t.id = e.team_id
		WHERE e.is_active = TRUE AND (e.team_id IS NULL OR e.team_id = $1)
		ORDER BY e.id`, *teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanExposedModelsWithTeam(rows)
}

func scanExposedModelWithTeam(row rowScanner) (*models.ExposedModel, error) {
	var m models.ExposedModel
	err := row.Scan(&m.ID, &m.ModelID, &m.OwnedBy, &m.IsActive,
		&m.TeamID, &m.TeamName,
		&m.LastOpenAITestTime, &m.LastAnthropicTestTime,
		&m.CreateTime, &m.UpdateTime)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan exposed with team: %w", err)
	}
	return &m, nil
}

func scanExposedModelsWithTeam(rows interface {
	Next() bool
	Err() error
	Scan(dest ...any) error
	Close()
}) ([]models.ExposedModel, error) {
	var out []models.ExposedModel
	for rows.Next() {
		var m models.ExposedModel
		if err := rows.Scan(&m.ID, &m.ModelID, &m.OwnedBy, &m.IsActive,
			&m.TeamID, &m.TeamName,
			&m.LastOpenAITestTime, &m.LastAnthropicTestTime,
			&m.CreateTime, &m.UpdateTime); err != nil {
			return nil, fmt.Errorf("scan exposed list with team: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
