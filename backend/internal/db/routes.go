package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/lys1313013/llm-gateway/backend/internal/models"
)

// ---------------------------------------------------------------------------
// Model route
// ---------------------------------------------------------------------------

const routeSelectCols = `
	r.id, r.model_pattern, r.route_type, r.provider_id, r.target_model,
	r.timeout, COALESCE(r.log_requests, TRUE), COALESCE(r.log_responses, TRUE), r.priority, r.is_active,
	r.team_id, t.name,
	r.create_time, r.update_time,
	p.openai_base_url, p.anthropic_base_url, p.responses_base_url, p.api_key, p.name
`

const routeFromJoin = `
	FROM model_route r
	LEFT JOIN provider p ON p.id = r.provider_id
	JOIN team t ON t.id = r.team_id
`

// GetRoutes 是某个团队视角的路由列表；GetAllRoutes 是 root 管理页的全量视角。
func GetRoutes(ctx context.Context, teamID int) ([]models.ModelRoute, error) {
	rows, err := mustHavePool().Query(ctx,
		`SELECT `+routeSelectCols+routeFromJoin+
			` WHERE r.team_id = $1 ORDER BY r.priority DESC, r.id ASC`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRoutes(rows)
}

func GetAllRoutes(ctx context.Context) ([]models.ModelRoute, error) {
	rows, err := mustHavePool().Query(ctx,
		`SELECT `+routeSelectCols+routeFromJoin+` ORDER BY r.priority DESC, r.id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRoutes(rows)
}

func GetRoute(ctx context.Context, id int) (*models.ModelRoute, error) {
	row := mustHavePool().QueryRow(ctx,
		`SELECT `+routeSelectCols+routeFromJoin+` WHERE r.id = $1`, id)
	r, err := scanRoute(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return r, nil
}

// GetActiveRoutes returns routes eligible for proxying: the route itself is
// active AND its provider (if still linked) is not disabled. 只返回调用者
// 所属团队的路由 —— 没有"公共路由"可以回退。
func GetActiveRoutes(ctx context.Context, teamID int) ([]models.ModelRoute, error) {
	rows, err := mustHavePool().Query(ctx,
		`SELECT `+routeSelectCols+routeFromJoin+
			` WHERE r.team_id = $1 AND r.is_active = TRUE AND (p.is_active IS NULL OR p.is_active = TRUE) ORDER BY r.priority DESC, r.id ASC`,
		teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRoutes(rows)
}

type CreateRouteInput struct {
	ModelPattern string  `json:"model_pattern"`
	RouteType    string  `json:"route_type"`
	ProviderID   *int    `json:"provider_id,omitempty"`
	TargetModel  *string `json:"target_model,omitempty"`
	Timeout      *int    `json:"timeout,omitempty"`
	LogRequests  *bool   `json:"log_requests,omitempty"`
	LogResponses *bool   `json:"log_responses,omitempty"`
	Priority     *int    `json:"priority,omitempty"`
	IsActive     *bool   `json:"is_active,omitempty"`
	TeamID       int     `json:"team_id"`
}

func CreateRoute(ctx context.Context, in CreateRouteInput) (*models.ModelRoute, error) {
	timeout := -1
	if in.Timeout != nil {
		timeout = *in.Timeout
	}
	logReq := true
	if in.LogRequests != nil {
		logReq = *in.LogRequests
	}
	logResp := true
	if in.LogResponses != nil {
		logResp = *in.LogResponses
	}
	prio := 0
	if in.Priority != nil {
		prio = *in.Priority
	}
	active := true
	if in.IsActive != nil {
		active = *in.IsActive
	}

	// Insert returns the id, then we fetch the joined record.
	var newID int
	err := mustHavePool().QueryRow(ctx, `
		INSERT INTO model_route (
			model_pattern, route_type, provider_id, target_model,
			timeout, log_requests, log_responses, priority, is_active, team_id
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id`,
		in.ModelPattern, in.RouteType, in.ProviderID, in.TargetModel,
		timeout, logReq, logResp, prio, active, in.TeamID).Scan(&newID)
	if err != nil {
		return nil, err
	}
	row := mustHavePool().QueryRow(ctx,
		`SELECT `+routeSelectCols+routeFromJoin+` WHERE r.id = $1`, newID)
	return scanRoute(row)
}

type UpdateRouteInput struct {
	ModelPattern  string  `json:"model_pattern"`
	RouteType     string  `json:"route_type"`
	ProviderID    *int    `json:"provider_id,omitempty"`
	TargetModel   *string `json:"target_model,omitempty"`
	Timeout       *int    `json:"timeout,omitempty"`
	LogRequests   *bool   `json:"log_requests,omitempty"`
	LogResponses  *bool   `json:"log_responses,omitempty"`
	Priority      *int    `json:"priority,omitempty"`
	IsActive      *bool   `json:"is_active,omitempty"`
	// 可选：不传则保持原归属（不能像其他字段那样直接覆盖成 NULL）
	TeamID *int `json:"team_id,omitempty"`
}

func UpdateRoute(ctx context.Context, id int, in UpdateRouteInput) (*models.ModelRoute, error) {
	// Update first, then re-select with the join (RETURNING can't include JOINs).
	_, err := mustHavePool().Exec(ctx, `
		UPDATE model_route SET
			model_pattern = $2, route_type = $3, provider_id = $4, target_model = $5,
			timeout = $6, log_requests = $7, log_responses = $8, priority = $9,
			is_active = $10, team_id = COALESCE($11, team_id), update_time = CURRENT_TIMESTAMP
		WHERE id = $1`,
		id, in.ModelPattern, in.RouteType, in.ProviderID, in.TargetModel,
		in.Timeout, in.LogRequests, in.LogResponses, in.Priority, in.IsActive, in.TeamID)
	if err != nil {
		return nil, err
	}
	row := mustHavePool().QueryRow(ctx,
		`SELECT `+routeSelectCols+routeFromJoin+` WHERE r.id = $1`, id)
	r, err := scanRoute(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return r, nil
}

func DeleteRoute(ctx context.Context, id int) error {
	tag, err := mustHavePool().Exec(ctx, `DELETE FROM model_route WHERE id = $1`, id)
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

func scanRoute(row rowScanner) (*models.ModelRoute, error) {
	var r models.ModelRoute
	err := row.Scan(
		&r.ID, &r.ModelPattern, &r.RouteType, &r.ProviderID, &r.TargetModel,
		&r.Timeout, &r.LogRequests, &r.LogResponses, &r.Priority, &r.IsActive,
		&r.TeamID, &r.TeamName,
		&r.CreateTime, &r.UpdateTime,
		&r.OpenAIBaseURL, &r.AnthropicBaseURL, &r.ResponsesBaseURL, &r.APIKey, &r.ProviderName,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan route: %w", err)
	}
	return &r, nil
}

func scanRoutes(rows interface {
	Next() bool
	Err() error
	Scan(dest ...any) error
	Close()
}) ([]models.ModelRoute, error) {
	var out []models.ModelRoute
	for rows.Next() {
		var r models.ModelRoute
		if err := rows.Scan(
			&r.ID, &r.ModelPattern, &r.RouteType, &r.ProviderID, &r.TargetModel,
			&r.Timeout, &r.LogRequests, &r.LogResponses, &r.Priority, &r.IsActive,
			&r.TeamID, &r.TeamName,
			&r.CreateTime, &r.UpdateTime,
			&r.OpenAIBaseURL, &r.AnthropicBaseURL, &r.ResponsesBaseURL, &r.APIKey, &r.ProviderName,
		); err != nil {
			return nil, fmt.Errorf("scan routes: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
