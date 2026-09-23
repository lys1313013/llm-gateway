package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/lys1313013/llm-gateway/backend/internal/models"
)

const teamSelectCols = `id, name, create_time, update_time`

func GetTeams(ctx context.Context) ([]models.Team, error) {
	rows, err := mustHavePool().Query(ctx,
		`SELECT `+teamSelectCols+` FROM team ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTeams(rows)
}

func GetTeam(ctx context.Context, id int) (*models.Team, error) {
	row := mustHavePool().QueryRow(ctx,
		`SELECT `+teamSelectCols+` FROM team WHERE id = $1`, id)
	t, err := scanTeam(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return t, nil
}

type CreateTeamInput struct {
	Name string `json:"name"`
}

func CreateTeam(ctx context.Context, in CreateTeamInput) (*models.Team, error) {
	row := mustHavePool().QueryRow(ctx, `
		INSERT INTO team (name) VALUES ($1)
		RETURNING `+teamSelectCols, in.Name)
	return scanTeam(row)
}

type UpdateTeamInput struct {
	Name string `json:"name"`
}

func UpdateTeam(ctx context.Context, id int, in UpdateTeamInput) (*models.Team, error) {
	row := mustHavePool().QueryRow(ctx, `
		UPDATE team SET name = $2, update_time = CURRENT_TIMESTAMP
		WHERE id = $1
		RETURNING `+teamSelectCols, id, in.Name)
	t, err := scanTeam(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return t, nil
}

func DeleteTeam(ctx context.Context, id int) error {
	tag, err := mustHavePool().Exec(ctx, `DELETE FROM team WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// TeamResourceCounts 是删除团队前的预检。RESTRICT 外键确实会拦住删除，但
// Postgres 抛出来的是裸的 23503，前端只能显示一串英文；先数一下才能拼出
// 「还有 N 个模型、M 条路由」这种能直接行动的提示。
func TeamResourceCounts(ctx context.Context, id int) (modelsCount, routesCount int, err error) {
	err = mustHavePool().QueryRow(ctx, `
		SELECT (SELECT COUNT(*) FROM exposed_model WHERE team_id = $1),
		       (SELECT COUNT(*) FROM model_route  WHERE team_id = $1)`, id).
		Scan(&modelsCount, &routesCount)
	return modelsCount, routesCount, err
}

// ---------------------------------------------------------------------------
// Scanners
// ---------------------------------------------------------------------------

func scanTeam(row rowScanner) (*models.Team, error) {
	var t models.Team
	err := row.Scan(&t.ID, &t.Name, &t.CreateTime, &t.UpdateTime)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan team: %w", err)
	}
	return &t, nil
}

func scanTeams(rows interface {
	Next() bool
	Err() error
	Scan(dest ...any) error
	Close()
}) ([]models.Team, error) {
	var out []models.Team
	for rows.Next() {
		var t models.Team
		if err := rows.Scan(&t.ID, &t.Name, &t.CreateTime, &t.UpdateTime); err != nil {
			return nil, fmt.Errorf("scan teams: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
