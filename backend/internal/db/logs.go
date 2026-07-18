package db

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/lys1313013/llm-gateway/backend/internal/config"
	"github.com/lys1313013/llm-gateway/backend/internal/models"
)

// ---------------------------------------------------------------------------
// Insert log — used by the proxy after each request
// ---------------------------------------------------------------------------

type InsertLogInput struct {
	Model                  *string
	ProviderID             *int
	ProviderName           *string
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
	SessionID              *string
	UserID                 *int
	LastMessagePreview     *string
}

func InsertLog(ctx context.Context, in InsertLogInput) error {
	_, err := mustHavePool().Exec(ctx, `
		INSERT INTO api_logs (
			model, provider_id, provider_name,
			is_stream, status_code, processing_time_ms,
			prompt_tokens, completion_tokens, total_tokens,
			cache_creation_input_tokens, cache_read_input_tokens,
			target_url, request_data, response_data,
			request_headers, response_headers,
			error_message, protocol, usage_data,
			session_id,
			user_id,
			last_message_preview
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22
		)`,
		in.Model, in.ProviderID, in.ProviderName,
		in.IsStream, in.StatusCode, in.ProcessingTimeMs,
		in.PromptTokens, in.CompletionTokens, in.TotalTokens,
		in.CacheCreationInputTokens, in.CacheReadInputTokens,
		in.TargetURL, jsonRawOrNil(in.RequestData), jsonRawOrNil(in.ResponseData),
		jsonRawOrNil(in.RequestHeaders), jsonRawOrNil(in.ResponseHeaders),
		in.ErrorMessage, in.Protocol, jsonRawOrNil(in.UsageData),
		in.SessionID, in.UserID,
		in.LastMessagePreview,
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
	Limit      int
	Offset     int
	Model      string
	Protocol   string
	StatusCode int
	UserID     int
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

	// List query omits request_data / response_data / request_headers /
	// response_headers — those JSONB columns are large, and the detail
	// endpoint (/api/logs/:id) returns them on demand. Mirrors the perf
	// optimization originally added in bf8576c for the Python backend.
	q := `SELECT id, created_at, updated_at, model, provider_id, provider_name,
	             is_stream, status_code,
	             processing_time_ms, prompt_tokens, completion_tokens, total_tokens,
	             target_url,
	             error_message, protocol,
	             usage_data, cache_creation_input_tokens, cache_read_input_tokens,
	             session_id,
	             user_id,
	             last_message_preview
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
	if f.StatusCode > 0 {
		q += fmt.Sprintf(" AND status_code = $%d", idx)
		args = append(args, f.StatusCode)
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
	row := mustHavePool().QueryRow(ctx, `SELECT id, created_at, updated_at, model, provider_id, provider_name,
	             is_stream, status_code,
	             processing_time_ms, prompt_tokens, completion_tokens, total_tokens,
	             target_url, request_data, response_data,
	             request_headers, response_headers,
	             error_message, protocol,
	             usage_data, cache_creation_input_tokens, cache_read_input_tokens,
	             session_id,
	             user_id,
	             last_message_preview
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

// DeleteLog removes a single log row. Returns ErrNotFound when no row matched.
func DeleteLog(ctx context.Context, id int) error {
	tag, err := mustHavePool().Exec(ctx, `DELETE FROM api_logs WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteLogsBySession removes every log row whose session_id matches. Returns
// ErrNotFound when the session had no logs (so the handler can return 404).
func DeleteLogsBySession(ctx context.Context, sessionID string) (int64, error) {
	tag, err := mustHavePool().Exec(ctx,
		`DELETE FROM api_logs WHERE session_id = $1`, sessionID)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() == 0 {
		return 0, ErrNotFound
	}
	return tag.RowsAffected(), nil
}

func GetLogCount(ctx context.Context, model, protocol string, statusCode int, userID int) (int, error) {
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
	if statusCode > 0 {
		q += fmt.Sprintf(" AND status_code = $%d", idx)
		args = append(args, statusCode)
		idx++
	}
	if userID > 0 {
		q += fmt.Sprintf(" AND user_id = $%d", idx)
		args = append(args, userID)
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

func GetTodayStats(ctx context.Context, userID int) (*TodayStats, error) {
	// "Today" is computed in the configured DB timezone, matching the
	// Python backend (default Asia/Shanghai).
	tz := config.Get().DBTimezone
	q := fmt.Sprintf(`
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE status_code BETWEEN 200 AND 299),
			COUNT(*) FILTER (WHERE status_code IS NULL OR status_code < 200 OR status_code >= 300),
			COALESCE(SUM(prompt_tokens), 0),
			COALESCE(SUM(completion_tokens), 0),
			COALESCE(SUM(total_tokens), 0)
		FROM api_logs
		WHERE DATE(created_at AT TIME ZONE '%s') = CURRENT_DATE`, tz)
	args := []any{}
	if userID > 0 {
		q += fmt.Sprintf(" AND user_id = $%d", 1)
		args = append(args, userID)
	}
	row := mustHavePool().QueryRow(ctx, q, args...)
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

func GetDailyTokenStats(ctx context.Context, startDate, endDate string, userID int) ([]DailyTokenStats, error) {
	// Build query with numbered params
	q := `
		SELECT
			TO_CHAR(DATE(created_at), 'YYYY-MM-DD') AS d,
			COUNT(id) AS request_count,
			COALESCE(SUM(prompt_tokens), 0),
			COALESCE(SUM(completion_tokens), 0),
			COALESCE(SUM(total_tokens), 0)
		FROM api_logs
		WHERE ($1::date IS NULL OR DATE(created_at) >= $1::date)
		  AND ($2::date IS NULL OR DATE(created_at) <= $2::date)`
	args := []any{nullDate(startDate), nullDate(endDate)}
	if userID > 0 {
		q += " AND user_id = $3"
		args = append(args, userID)
	}
	q += " GROUP BY DATE(created_at) ORDER BY DATE(created_at) ASC"
	rows, err := mustHavePool().Query(ctx, q, args...)
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

func GetHourlyTokenStats(ctx context.Context, date string, userID int) ([]HourlyTokenStats, error) {
	// Python uses DB_TIMEZONE (default Asia/Shanghai) for both the date
	// filter and the hour extraction, and pads the 24-hour series with
	// zero rows via generate_series. We mirror that here.
	tz := config.Get().DBTimezone
	userFilter := ""
	if userID > 0 {
		userFilter = " AND user_id = $2"
	}
	q := fmt.Sprintf(`
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
			WHERE DATE(created_at AT TIME ZONE '%s') = $1::date%s
			GROUP BY EXTRACT(HOUR FROM created_at AT TIME ZONE '%s')
		) s ON s.hour = g.hour
		ORDER BY g.hour ASC`, tz, tz, userFilter, tz)
	args := []any{nullDate(date)}
	if userID > 0 {
		args = append(args, userID)
	}
	rows, err := mustHavePool().Query(ctx, q, args...)
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

func GetModelTokenStats(ctx context.Context, startDate, endDate string, userID int) ([]ModelTokenStats, error) {
	q := `
		SELECT
			COALESCE(model, 'unknown') AS m,
			COUNT(id) AS request_count,
			COALESCE(SUM(prompt_tokens), 0),
			COALESCE(SUM(completion_tokens), 0),
			COALESCE(SUM(total_tokens), 0)
		FROM api_logs
		WHERE ($1::date IS NULL OR DATE(created_at) >= $1::date)
		  AND ($2::date IS NULL OR DATE(created_at) <= $2::date)`
	args := []any{nullDate(startDate), nullDate(endDate)}
	if userID > 0 {
		q += " AND user_id = $3"
		args = append(args, userID)
	}
	q += " GROUP BY model ORDER BY SUM(total_tokens) DESC"
	rows, err := mustHavePool().Query(ctx, q, args...)
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

// GetDistinctStatusCodes returns the distinct, non-NULL status codes that
// appear in api_logs, ordered from most to least frequent so that the most
// relevant codes show up first in the UI filter.
func GetDistinctStatusCodes(ctx context.Context) ([]int, error) {
	rows, err := mustHavePool().Query(ctx, `
		SELECT status_code
		FROM api_logs
		WHERE status_code IS NOT NULL
		GROUP BY status_code
		ORDER BY COUNT(*) DESC, status_code ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var codes []int
	for rows.Next() {
		var c int
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		codes = append(codes, c)
	}
	return codes, rows.Err()
}

// ---------------------------------------------------------------------------
// Scanners
// ---------------------------------------------------------------------------

func scanLog(row rowScanner) (*models.APILog, error) {
	var l models.APILog
	err := row.Scan(
		&l.ID, &l.CreatedAt, &l.UpdatedAt, &l.Model, &l.ProviderID, &l.ProviderName,
		&l.IsStream, &l.StatusCode,
		&l.ProcessingTimeMs, &l.PromptTokens, &l.CompletionTokens, &l.TotalTokens,
		&l.TargetURL, &l.RequestData, &l.ResponseData,
		&l.RequestHeaders, &l.ResponseHeaders,
		&l.ErrorMessage, &l.Protocol,
		&l.UsageData, &l.CacheCreationInputTokens, &l.CacheReadInputTokens,
		&l.SessionID, &l.UserID,
		&l.LastMessagePreview,
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
			&l.ID, &l.CreatedAt, &l.UpdatedAt, &l.Model, &l.ProviderID, &l.ProviderName,
			&l.IsStream, &l.StatusCode,
			&l.ProcessingTimeMs, &l.PromptTokens, &l.CompletionTokens, &l.TotalTokens,
			&l.TargetURL,
			&l.ErrorMessage, &l.Protocol,
			&l.UsageData, &l.CacheCreationInputTokens, &l.CacheReadInputTokens,
			&l.SessionID, &l.UserID,
			&l.LastMessagePreview,
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

// ---------------------------------------------------------------------------
// Sessions — 按 session_id（由配置请求头解析）对 api_logs 进行聚合
// ---------------------------------------------------------------------------

// SessionSummary is one row in the session list. Models and StatusSummary
// are populated separately by the handler so the list query stays simple.
type SessionSummary struct {
	SessionID        string         `json:"session_id"`
	RequestCount     int            `json:"request_count"`
	PromptTokens     int            `json:"prompt_tokens"`
	CompletionTokens int            `json:"completion_tokens"`
	TotalTokens      int            `json:"total_tokens"`
	FirstAt          *time.Time     `json:"first_at,omitempty"`
	LastAt           *time.Time     `json:"last_at,omitempty"`
	Models           []string       `json:"models,omitempty"`
	StatusSummary    map[string]int `json:"status_summary,omitempty"`
	ProtocolSummary  map[string]int `json:"protocol_summary,omitempty"`
}

// SessionMeta is the header card data for the session detail page. It
// reuses the aggregate fields from SessionSummary and exposes them
// without the per-session list-view extras.
type SessionMeta struct {
	SessionID        string         `json:"session_id"`
	RequestCount     int            `json:"request_count"`
	PromptTokens     int            `json:"prompt_tokens"`
	CompletionTokens int            `json:"completion_tokens"`
	TotalTokens      int            `json:"total_tokens"`
	FirstAt          *time.Time     `json:"first_at,omitempty"`
	LastAt           *time.Time     `json:"last_at,omitempty"`
	Models           []string       `json:"models,omitempty"`
	StatusSummary    map[string]int `json:"status_summary,omitempty"`
	ProtocolSummary  map[string]int `json:"protocol_summary,omitempty"`
}

type SessionsListFilter struct {
	Query  string // optional ILIKE match on session_id
	Limit  int
	Offset int
	UserID int
}

func GetSessions(ctx context.Context, f SessionsListFilter) ([]SessionSummary, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 500 {
		limit = 500
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	q := `SELECT session_id,
	             COUNT(*) AS request_count,
	             COALESCE(SUM(prompt_tokens), 0),
	             COALESCE(SUM(completion_tokens), 0),
	             COALESCE(SUM(total_tokens), 0),
	             MIN(created_at) AS first_at,
	             MAX(created_at) AS last_at
	      FROM api_logs
	      WHERE session_id IS NOT NULL`
	args := []any{}
	idx := 1
	if f.Query != "" {
		q += fmt.Sprintf(" AND session_id ILIKE $%d", idx)
		args = append(args, "%"+f.Query+"%")
		idx++
	}
	if f.UserID > 0 {
		q += fmt.Sprintf(" AND user_id = $%d", idx)
		args = append(args, f.UserID)
		idx++
	}
	q += fmt.Sprintf(" GROUP BY session_id ORDER BY MAX(created_at) DESC LIMIT $%d OFFSET $%d", idx, idx+1)
	args = append(args, limit, offset)

	rows, err := mustHavePool().Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SessionSummary, 0)
	for rows.Next() {
		var s SessionSummary
		if err := rows.Scan(
			&s.SessionID, &s.RequestCount,
			&s.PromptTokens, &s.CompletionTokens, &s.TotalTokens,
			&s.FirstAt, &s.LastAt,
		); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func GetSessionCount(ctx context.Context, query string) (int, error) {
	q := `SELECT COUNT(*) FROM (SELECT session_id FROM api_logs WHERE session_id IS NOT NULL`
	args := []any{}
	if query != "" {
		q += " AND session_id ILIKE $1"
		args = append(args, "%"+query+"%")
	}
	q += " GROUP BY session_id) t"
	var n int
	err := mustHavePool().QueryRow(ctx, q, args...).Scan(&n)
	return n, err
}

// GetLogsBySession returns logs for a single session in chronological
// order (id ASC) so the detail page reads top-to-bottom like a transcript.
func GetLogsBySession(ctx context.Context, sessionID string, limit, offset int) ([]models.APILog, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := mustHavePool().Query(ctx, `
		SELECT id, created_at, updated_at, model, provider_id, provider_name,
		       is_stream, status_code,
		       processing_time_ms, prompt_tokens, completion_tokens, total_tokens,
		       target_url,
		       error_message, protocol,
		       usage_data, cache_creation_input_tokens, cache_read_input_tokens,,
		       last_message_preview
		       session_id,
		       user_id
		FROM api_logs
		WHERE session_id = $1
		ORDER BY id ASC
		LIMIT $2 OFFSET $3`, sessionID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanLogs(rows)
}

func GetLogCountBySession(ctx context.Context, sessionID string) (int, error) {
	var n int
	err := mustHavePool().QueryRow(ctx,
		`SELECT COUNT(*) FROM api_logs WHERE session_id = $1`, sessionID).Scan(&n)
	return n, err
}

func GetSessionMeta(ctx context.Context, sessionID string) (*SessionMeta, error) {
	row := mustHavePool().QueryRow(ctx, `
		SELECT COUNT(*),
		       COALESCE(SUM(prompt_tokens), 0),
		       COALESCE(SUM(completion_tokens), 0),
		       COALESCE(SUM(total_tokens), 0),
		       MIN(created_at), MAX(created_at)
		FROM api_logs WHERE session_id = $1`, sessionID)
	m := SessionMeta{SessionID: sessionID}
	if err := row.Scan(
		&m.RequestCount,
		&m.PromptTokens, &m.CompletionTokens, &m.TotalTokens,
		&m.FirstAt, &m.LastAt,
	); err != nil {
		return nil, err
	}
	if m.RequestCount == 0 {
		return nil, nil
	}
	return &m, nil
}

// GetDistinctSessionModels returns the unique, non-NULL model names for a
// session, ordered by frequency. Capped at 100 to keep the UI list
// manageable.
func GetDistinctSessionModels(ctx context.Context, sessionID string) ([]string, error) {
	rows, err := mustHavePool().Query(ctx, `
		SELECT model
		FROM api_logs
		WHERE session_id = $1 AND model IS NOT NULL
		GROUP BY model
		ORDER BY COUNT(*) DESC, model ASC
		LIMIT 100`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func GetSessionStatusSummary(ctx context.Context, sessionID string) (map[string]int, error) {
	rows, err := mustHavePool().Query(ctx, `
		SELECT status_code, COUNT(*)
		FROM api_logs
		WHERE session_id = $1 AND status_code IS NOT NULL
		GROUP BY status_code
		ORDER BY status_code ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var code, count int
		if err := rows.Scan(&code, &count); err != nil {
			return nil, err
		}
		out[strconv.Itoa(code)] = count
	}
	return out, rows.Err()
}

func GetSessionProtocolSummary(ctx context.Context, sessionID string) (map[string]int, error) {
	rows, err := mustHavePool().Query(ctx, `
		SELECT protocol, COUNT(*)
		FROM api_logs
		WHERE session_id = $1 AND protocol IS NOT NULL
		GROUP BY protocol
		ORDER BY protocol ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var proto string
		var count int
		if err := rows.Scan(&proto, &count); err != nil {
			return nil, err
		}
		out[proto] = count
	}
	return out, rows.Err()
}
