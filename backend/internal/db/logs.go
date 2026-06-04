package db

import (
	"context"
	"fmt"
	"time"

	"github.com/lys1313013/llm-gateway/backend/internal/config"
	"github.com/lys1313013/llm-gateway/backend/internal/models"
)

// ---------------------------------------------------------------------------
// Insert log — used by the proxy after each request
// ---------------------------------------------------------------------------

type InsertLogInput struct {
	Model                  *string
	IsStream               bool
	StatusCode             int
	ProcessingTimeMs       int
	PromptTokens           *int
	CompletionTokens       *int
	TotalTokens            *int
	CacheCreationInputTokens *int
	CacheReadInputTokens   *int
	TargetURL              *string
	RequestData            []byte // JSON
	ResponseData           []byte // JSON
	RequestHeaders         []byte // JSON
	ResponseHeaders        []byte // JSON
	ErrorMessage           *string
	Protocol               *string
	UsageData              []byte // JSON
}

func InsertLog(ctx context.Context, in InsertLogInput) error {
	_, err := mustHavePool().Exec(ctx, `
		INSERT INTO api_logs (
			model, is_stream, status_code, processing_time_ms,
			prompt_tokens, completion_tokens, total_tokens,
			cache_creation_input_tokens, cache_read_input_tokens,
			target_url, request_data, response_data,
			request_headers, response_headers,
			error_message, protocol, usage_data
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17
		)`,
		in.Model, in.IsStream, in.StatusCode, in.ProcessingTimeMs,
		in.PromptTokens, in.CompletionTokens, in.TotalTokens,
		in.CacheCreationInputTokens, in.CacheReadInputTokens,
		in.TargetURL, jsonRawOrNil(in.RequestData), jsonRawOrNil(in.ResponseData),
		jsonRawOrNil(in.RequestHeaders), jsonRawOrNil(in.ResponseHeaders),
		in.ErrorMessage, in.Protocol, jsonRawOrNil(in.UsageData),
	)
	return err
}

func jsonRawOrNil(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// ---------------------------------------------------------------------------
// List / detail / count
// ---------------------------------------------------------------------------

type LogListFilter struct {
	Limit    int
	Offset   int
	Model    string
	Protocol string
}

func GetLogs(ctx context.Context, f LogListFilter) ([]models.APILog, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	q := `SELECT id, created_at, updated_at, model, is_stream, status_code,
	             processing_time_ms, prompt_tokens, completion_tokens, total_tokens,
	             target_url, request_data, response_data,
	             request_headers, response_headers,
	             error_message, protocol,
	             usage_data, cache_creation_input_tokens, cache_read_input_tokens
	      FROM api_logs WHERE 1=1`
	args := []any{}
	idx := 1
	if f.Model != "" {
		q += fmt.Sprintf(" AND model ILIKE $%d", idx)
		args = append(args, "%"+f.Model+"%")
		idx++
	}
	if f.Protocol != "" {
		q += fmt.Sprintf(" AND protocol = $%d", idx)
		args = append(args, f.Protocol)
		idx++
	}
	q += fmt.Sprintf(" ORDER BY id DESC LIMIT $%d OFFSET $%d", idx, idx+1)
	args = append(args, limit, offset)

	rows, err := mustHavePool().Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanLogs(rows)
}

func GetLogByID(ctx context.Context, id int) (*models.APILog, error) {
	row := mustHavePool().QueryRow(ctx, `SELECT id, created_at, updated_at, model, is_stream, status_code,
	             processing_time_ms, prompt_tokens, completion_tokens, total_tokens,
	             target_url, request_data, response_data,
	             request_headers, response_headers,
	             error_message, protocol,
	             usage_data, cache_creation_input_tokens, cache_read_input_tokens
	         FROM api_logs WHERE id = $1`, id)
	l, err := scanLog(row)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, err
	}
	return l, nil
}

func GetLogCount(ctx context.Context, model, protocol string) (int, error) {
	q := `SELECT COUNT(*) FROM api_logs WHERE 1=1`
	args := []any{}
	idx := 1
	if model != "" {
		q += fmt.Sprintf(" AND model ILIKE $%d", idx)
		args = append(args, "%"+model+"%")
		idx++
	}
	if protocol != "" {
		q += fmt.Sprintf(" AND protocol = $%d", idx)
		args = append(args, protocol)
		idx++
	}
	var n int
	err := mustHavePool().QueryRow(ctx, q, args...).Scan(&n)
	return n, err
}

// ---------------------------------------------------------------------------
// Stats
// ---------------------------------------------------------------------------

type TodayStats struct {
	TotalRequests    int   `json:"total_requests"`
	SuccessRequests  int   `json:"success_requests"`
	ErrorRequests    int   `json:"error_requests"`
	PromptTokens     int   `json:"prompt_tokens"`
	CompletionTokens int   `json:"completion_tokens"`
	TotalTokens      int   `json:"total_tokens"`
}

func GetTodayStats(ctx context.Context) (*TodayStats, error) {
	// "Today" is computed in the configured DB timezone, matching the
	// Python backend (default Asia/Shanghai).
	tz := config.Get().DBTimezone
	row := mustHavePool().QueryRow(ctx, fmt.Sprintf(`
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE status_code BETWEEN 200 AND 299),
			COUNT(*) FILTER (WHERE status_code IS NULL OR status_code < 200 OR status_code >= 300),
			COALESCE(SUM(prompt_tokens), 0),
			COALESCE(SUM(completion_tokens), 0),
			COALESCE(SUM(total_tokens), 0)
		FROM api_logs
		WHERE DATE(created_at AT TIME ZONE '%s') = CURRENT_DATE`, tz))
	var s TodayStats
	if err := row.Scan(&s.TotalRequests, &s.SuccessRequests, &s.ErrorRequests,
		&s.PromptTokens, &s.CompletionTokens, &s.TotalTokens); err != nil {
		return nil, err
	}
	return &s, nil
}

type DailyTokenStats struct {
	Date             string `json:"date"`
	RequestCount     int    `json:"request_count"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	TotalTokens      int    `json:"total_tokens"`
}

func GetDailyTokenStats(ctx context.Context, startDate, endDate string) ([]DailyTokenStats, error) {
	rows, err := mustHavePool().Query(ctx, `
		SELECT
			TO_CHAR(DATE(created_at), 'YYYY-MM-DD') AS d,
			COUNT(id) AS request_count,
			COALESCE(SUM(prompt_tokens), 0),
			COALESCE(SUM(completion_tokens), 0),
			COALESCE(SUM(total_tokens), 0)
		FROM api_logs
		WHERE ($1::date IS NULL OR DATE(created_at) >= $1::date)
		  AND ($2::date IS NULL OR DATE(created_at) <= $2::date)
		GROUP BY DATE(created_at)
		ORDER BY DATE(created_at) ASC`,
		nullDate(startDate), nullDate(endDate))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DailyTokenStats
	for rows.Next() {
		var s DailyTokenStats
		if err := rows.Scan(&s.Date, &s.RequestCount, &s.PromptTokens, &s.CompletionTokens, &s.TotalTokens); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

type HourlyTokenStats struct {
	Hour             int `json:"hour"`
	RequestCount     int `json:"request_count"`
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func GetHourlyTokenStats(ctx context.Context, date string) ([]HourlyTokenStats, error) {
	// Python uses DB_TIMEZONE (default Asia/Shanghai) for both the date
	// filter and the hour extraction, and pads the 24-hour series with
	// zero rows via generate_series. We mirror that here.
	tz := config.Get().DBTimezone
	rows, err := mustHavePool().Query(ctx, fmt.Sprintf(`
		SELECT
			g.hour AS hour,
			COALESCE(s.request_count, 0)     AS request_count,
			COALESCE(s.prompt_tokens, 0)     AS prompt_tokens,
			COALESCE(s.completion_tokens, 0) AS completion_tokens,
			COALESCE(s.total_tokens, 0)      AS total_tokens
		FROM generate_series(0, 23) AS g(hour)
		LEFT JOIN (
			SELECT
				EXTRACT(HOUR FROM created_at AT TIME ZONE '%s')::int AS hour,
				COUNT(id)              AS request_count,
				SUM(prompt_tokens)     AS prompt_tokens,
				SUM(completion_tokens) AS completion_tokens,
				SUM(total_tokens)      AS total_tokens
			FROM api_logs
			WHERE DATE(created_at AT TIME ZONE '%s') = $1::date
			GROUP BY EXTRACT(HOUR FROM created_at AT TIME ZONE '%s')
		) s ON s.hour = g.hour
		ORDER BY g.hour ASC`, tz, tz, tz),
		nullDate(date))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HourlyTokenStats
	for rows.Next() {
		var s HourlyTokenStats
		if err := rows.Scan(&s.Hour, &s.RequestCount, &s.PromptTokens, &s.CompletionTokens, &s.TotalTokens); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

type ModelTokenStats struct {
	Model            string `json:"model"`
	RequestCount     int    `json:"request_count"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	TotalTokens      int    `json:"total_tokens"`
}

func GetModelTokenStats(ctx context.Context, startDate, endDate string) ([]ModelTokenStats, error) {
	rows, err := mustHavePool().Query(ctx, `
		SELECT
			COALESCE(model, 'unknown') AS m,
			COUNT(id) AS request_count,
			COALESCE(SUM(prompt_tokens), 0),
			COALESCE(SUM(completion_tokens), 0),
			COALESCE(SUM(total_tokens), 0)
		FROM api_logs
		WHERE ($1::date IS NULL OR DATE(created_at) >= $1::date)
		  AND ($2::date IS NULL OR DATE(created_at) <= $2::date)
		GROUP BY model
		ORDER BY SUM(total_tokens) DESC`,
		nullDate(startDate), nullDate(endDate))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelTokenStats
	for rows.Next() {
		var s ModelTokenStats
		if err := rows.Scan(&s.Model, &s.RequestCount, &s.PromptTokens, &s.CompletionTokens, &s.TotalTokens); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func nullDate(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ---------------------------------------------------------------------------
// Scanners
// ---------------------------------------------------------------------------

func scanLog(row rowScanner) (*models.APILog, error) {
	var l models.APILog
	err := row.Scan(
		&l.ID, &l.CreatedAt, &l.UpdatedAt, &l.Model, &l.IsStream, &l.StatusCode,
		&l.ProcessingTimeMs, &l.PromptTokens, &l.CompletionTokens, &l.TotalTokens,
		&l.TargetURL, &l.RequestData, &l.ResponseData,
		&l.RequestHeaders, &l.ResponseHeaders,
		&l.ErrorMessage, &l.Protocol,
		&l.UsageData, &l.CacheCreationInputTokens, &l.CacheReadInputTokens,
	)
	if err != nil {
		return nil, err
	}
	return &l, nil
}

func scanLogs(rows interface {
	Next() bool
	Err() error
	Scan(dest ...any) error
	Close()
}) ([]models.APILog, error) {
	var out []models.APILog
	for rows.Next() {
		var l models.APILog
		if err := rows.Scan(
			&l.ID, &l.CreatedAt, &l.UpdatedAt, &l.Model, &l.IsStream, &l.StatusCode,
			&l.ProcessingTimeMs, &l.PromptTokens, &l.CompletionTokens, &l.TotalTokens,
			&l.TargetURL, &l.RequestData, &l.ResponseData,
			&l.RequestHeaders, &l.ResponseHeaders,
			&l.ErrorMessage, &l.Protocol,
			&l.UsageData, &l.CacheCreationInputTokens, &l.CacheReadInputTokens,
		); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// avoid "imported and not used" if a build tag strips callers
var _ = time.Now
