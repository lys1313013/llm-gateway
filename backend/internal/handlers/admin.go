package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lys1313013/llm-gateway/backend/internal/config"
	"github.com/lys1313013/llm-gateway/backend/internal/db"
	"github.com/lys1313013/llm-gateway/backend/internal/middleware"
	"github.com/lys1313013/llm-gateway/backend/internal/models"
	"github.com/lys1313013/llm-gateway/backend/internal/quota"
)

// ---------------------------------------------------------------------------
// Provider CRUD
// ---------------------------------------------------------------------------

func ListProviders(c *gin.Context) {
	middleware.RequireAdmin(c)
	if c.IsAborted() {
		return
	}
	xs, err := db.GetProviders(c.Request.Context())
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, xs)
}

func GetProvider(c *gin.Context) {
	middleware.RequireAdmin(c)
	if c.IsAborted() {
		return
	}
	id, _ := strconv.Atoi(c.Param("id"))
	p, err := db.GetProvider(c.Request.Context(), id)
	if errors.Is(err, db.ErrNotFound) || p == nil {
		notFound(c, "Not found")
		return
	}
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, p)
}

func CreateProvider(c *gin.Context) {
	middleware.RequireAdmin(c)
	if c.IsAborted() {
		return
	}
	var in db.CreateProviderInput
	if !bindJSON(c, &in) {
		return
	}
	p, err := db.CreateProvider(c.Request.Context(), in)
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, p)
}

func UpdateProvider(c *gin.Context) {
	middleware.RequireAdmin(c)
	if c.IsAborted() {
		return
	}
	id, _ := strconv.Atoi(c.Param("id"))
	var in db.UpdateProviderInput
	if !bindJSON(c, &in) {
		return
	}
	p, err := db.UpdateProvider(c.Request.Context(), id, in)
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, p)
}

func DeleteProvider(c *gin.Context) {
	middleware.RequireAdmin(c)
	if c.IsAborted() {
		return
	}
	id, _ := strconv.Atoi(c.Param("id"))
	if err := db.DeleteProvider(c.Request.Context(), id); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ListProviderPresets returns the preset catalog used to pre-fill the
// new-provider form. Order matches the file. An empty list is a valid
// response — the UI just hides the preset selector.
func ListProviderPresets(c *gin.Context) {
	middleware.RequireAdmin(c)
	if c.IsAborted() {
		return
	}
	presets, err := config.LoadPresets()
	if err != nil {
		// A missing file is non-fatal: the UI just won't offer presets.
		slog.Warn("presets unavailable", "err", err)
		ok(c, []any{})
		return
	}
	ok(c, presets)
}

// ---------------------------------------------------------------------------
// Model route CRUD
// ---------------------------------------------------------------------------

func ListRoutes(c *gin.Context) {
	middleware.RequireAdmin(c)
	if c.IsAborted() {
		return
	}
	xs, err := db.GetRoutes(c.Request.Context())
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, xs)
}

func GetRoute(c *gin.Context) {
	middleware.RequireAdmin(c)
	if c.IsAborted() {
		return
	}
	id, _ := strconv.Atoi(c.Param("id"))
	r, err := db.GetRoute(c.Request.Context(), id)
	if errors.Is(err, db.ErrNotFound) || r == nil {
		notFound(c, "Not found")
		return
	}
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, r)
}

func CreateRoute(c *gin.Context) {
	middleware.RequireAdmin(c)
	if c.IsAborted() {
		return
	}
	var in db.CreateRouteInput
	if !bindJSON(c, &in) {
		return
	}
	r, err := db.CreateRoute(c.Request.Context(), in)
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, r)
}

func UpdateRoute(c *gin.Context) {
	middleware.RequireAdmin(c)
	if c.IsAborted() {
		return
	}
	id, _ := strconv.Atoi(c.Param("id"))
	var in db.UpdateRouteInput
	if !bindJSON(c, &in) {
		return
	}
	r, err := db.UpdateRoute(c.Request.Context(), id, in)
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, r)
}

func DeleteRoute(c *gin.Context) {
	middleware.RequireAdmin(c)
	if c.IsAborted() {
		return
	}
	id, _ := strconv.Atoi(c.Param("id"))
	if err := db.DeleteRoute(c.Request.Context(), id); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ---------------------------------------------------------------------------
// Exposed model CRUD
// ---------------------------------------------------------------------------

func ListExposedModels(c *gin.Context) {
	var xs []models.ExposedModel
	var err error
	if middleware.GetUserRole(c) == 1 {
		xs, err = db.GetExposedModels(c.Request.Context())
	} else {
		teamID := middleware.GetTeamID(c)
		xs, err = db.GetExposedModelsForTeam(c.Request.Context(), teamID)
	}
	if err != nil {
		serverError(c, err)
		return
	}
	if xs == nil {
		xs = []models.ExposedModel{}
	}
	ok(c, xs)
}

func GetExposedModel(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	m, err := db.GetExposedModel(c.Request.Context(), id)
	if errors.Is(err, db.ErrNotFound) || m == nil {
		notFound(c, "Not found")
		return
	}
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, m)
}

func CreateExposedModel(c *gin.Context) {
	middleware.RequireAdmin(c)
	if c.IsAborted() {
		return
	}
	var in db.CreateExposedModelInput
	if !bindJSON(c, &in) {
		return
	}
	if existing, _ := db.GetExposedModelByName(c.Request.Context(), in.ModelID); existing != nil {
		c.JSON(http.StatusConflict, gin.H{
			"success": false,
			"message": "model_id '" + in.ModelID + "' 已存在，不能重复添加",
		})
		return
	}
	m, err := db.CreateExposedModel(c.Request.Context(), in)
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, m)
}

func UpdateExposedModel(c *gin.Context) {
	middleware.RequireAdmin(c)
	if c.IsAborted() {
		return
	}
	id, _ := strconv.Atoi(c.Param("id"))
	var in db.UpdateExposedModelInput
	if !bindJSON(c, &in) {
		return
	}
	m, err := db.UpdateExposedModel(c.Request.Context(), id, in)
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, m)
}

func DeleteExposedModel(c *gin.Context) {
	middleware.RequireAdmin(c)
	if c.IsAborted() {
		return
	}
	id, _ := strconv.Atoi(c.Param("id"))
	if err := db.DeleteExposedModel(c.Request.Context(), id); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func UpdateExposedModelTestTime(c *gin.Context) {
	middleware.RequireAdmin(c)
	if c.IsAborted() {
		return
	}
	id, _ := strconv.Atoi(c.Param("id"))
	var in struct {
		Protocol string `json:"protocol"`
	}
	if !bindJSON(c, &in) {
		return
	}
	if in.Protocol != "openai" && in.Protocol != "anthropic" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "protocol must be openai or anthropic",
		})
		return
	}
	m, err := db.UpdateExposedModelTestTime(c.Request.Context(), id, in.Protocol)
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, m)
}

// ---------------------------------------------------------------------------
// Quota
// ---------------------------------------------------------------------------

// ListProviderQuotas returns the cached quota snapshot for every provider.
// The frontend uses this for the top-of-page overview card.
func ListProviderQuotas(c *gin.Context) {
	middleware.RequireAdmin(c)
	if c.IsAborted() {
		return
	}
	providers, err := db.GetProviders(c.Request.Context())
	if err != nil {
		serverError(c, err)
		return
	}
	cache := quota.Global().Cache
	out := make([]gin.H, 0, len(providers))
	for _, p := range providers {
		snap, ok := cache.Get(p.ID)
		out = append(out, gin.H{
			"provider_id":   p.ID,
			"provider_name": p.Name,
			"has_config":    p.QuotaURL != nil && *p.QuotaURL != "",
			"snapshot":      snap,
			"present":       ok,
		})
	}
	ok(c, out)
}

// GetProviderQuota returns the cached snapshot for a single provider.
func GetProviderQuota(c *gin.Context) {
	middleware.RequireAdmin(c)
	if c.IsAborted() {
		return
	}
	id, _ := strconv.Atoi(c.Param("id"))
	snap, cached := quota.Global().Cache.Get(id)
	if !cached {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "no quota snapshot cached for this provider",
		})
		return
	}
	ok(c, gin.H{"provider_id": id, "snapshot": snap, "present": true})
}

// RefreshProviderQuota synchronously re-fetches quota for a single provider
// and updates the cache. Uses the request context so the client can cancel.
func RefreshProviderQuota(c *gin.Context) {
	middleware.RequireAdmin(c)
	if c.IsAborted() {
		return
	}
	id, _ := strconv.Atoi(c.Param("id"))
	p, err := db.GetProvider(c.Request.Context(), id)
	if err != nil {
		serverError(c, err)
		return
	}
	if p == nil {
		notFound(c, "provider not found")
		return
	}
	if p.QuotaURL == nil || *p.QuotaURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "provider has no quota_url configured",
		})
		return
	}
	quota.Global().RefreshOne(c.Request.Context(), *p)
	snap, _ := quota.Global().Cache.Get(id)
	if snap.LastError != "" {
		c.JSON(http.StatusBadGateway, gin.H{
			"success":  false,
			"message":  snap.LastError,
			"snapshot": snap,
		})
		return
	}
	ok(c, gin.H{"provider_id": id, "snapshot": snap, "present": true})
}

// ---------------------------------------------------------------------------
// Logs / stats
// ---------------------------------------------------------------------------

func ListLogs(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	model := c.Query("model")
	protocol := c.Query("protocol")
	statusCode, _ := strconv.Atoi(c.Query("status_code"))
	if model == "" {
		model = c.Query("model_name") // support both
	}
	userIDForFilter := 0
	if middleware.GetUserRole(c) > 2 {
		userIDForFilter = c.GetInt(middleware.CtxUserID)
	}
	filter := db.LogListFilter{
		Limit:      limit,
		Offset:     offset,
		Model:      model,
		Protocol:   protocol,
		StatusCode: statusCode,
		UserID:     userIDForFilter,
	}
	logs, err := db.GetLogs(c.Request.Context(), filter)
	if err != nil {
		serverError(c, err)
		return
	}
	total, _ := db.GetLogCount(c.Request.Context(), filter.Model, filter.Protocol, filter.StatusCode, userIDForFilter)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": logs, "total": total})
}

func GetLogDetail(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	log, err := db.GetLogByID(c.Request.Context(), id)
	if err != nil {
		serverError(c, err)
		return
	}
	if log == nil {
		notFound(c, "Not found")
		return
	}
	if middleware.GetUserRole(c) > 2 && log.UserID != nil && *log.UserID != c.GetInt(middleware.CtxUserID) {
		notFound(c, "Not found")
		return
	}
	// json.RawMessage already serializes as JSON; pass through.
	c.JSON(http.StatusOK, gin.H{"success": true, "data": log})
}

// DeleteLog removes a single log row. Logs are append-only audit data, so
// this is intended for cleanup of noisy / test runs, not routine use.
func DeleteLog(c *gin.Context) {
	middleware.RequireAdmin(c)
	if c.IsAborted() {
		return
	}
	id, _ := strconv.Atoi(c.Param("id"))
	if id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid log id"})
		return
	}
	if err := db.DeleteLog(c.Request.Context(), id); err != nil {
		if err == db.ErrNotFound {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Log not found"})
			return
		}
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "已删除"})
}

// ---------------------------------------------------------------------------
// Sessions — 按 session_id（由配置请求头解析）对 api_logs 进行聚合
// ---------------------------------------------------------------------------

// ListSessions returns one row per distinct non-NULL session_id, ordered
// by most recent activity. The handler enriches each row with the distinct
// model list and status code distribution so the list query stays
// simple at the SQL layer.
func ListSessions(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	q := c.Query("q")

	sessions, err := db.GetSessions(c.Request.Context(), db.SessionsListFilter{
		Query: q, Limit: limit, Offset: offset,
	})
	if err != nil {
		serverError(c, err)
		return
	}

	for i := range sessions {
		models, mErr := db.GetDistinctSessionModels(c.Request.Context(), sessions[i].SessionID)
		if mErr == nil {
			sessions[i].Models = models
		}
		statuses, sErr := db.GetSessionStatusSummary(c.Request.Context(), sessions[i].SessionID)
		if sErr == nil {
			sessions[i].StatusSummary = statuses
		}
		protos, pErr := db.GetSessionProtocolSummary(c.Request.Context(), sessions[i].SessionID)
		if pErr == nil {
			sessions[i].ProtocolSummary = protos
		}
	}

	total, _ := db.GetSessionCount(c.Request.Context(), q)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": sessions, "total": total})
}

// GetSession returns all logs belonging to a single session in
// chronological order (id ASC), plus the aggregate meta used by the
// detail page header.
func GetSession(c *gin.Context) {
	sessionID := c.Param("id")
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "session id required"})
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	meta, err := db.GetSessionMeta(c.Request.Context(), sessionID)
	if err != nil {
		serverError(c, err)
		return
	}
	if meta == nil || meta.RequestCount == 0 {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Session not found"})
		return
	}

	models, _ := db.GetDistinctSessionModels(c.Request.Context(), sessionID)
	meta.Models = models
	statuses, _ := db.GetSessionStatusSummary(c.Request.Context(), sessionID)
	meta.StatusSummary = statuses
	protos, _ := db.GetSessionProtocolSummary(c.Request.Context(), sessionID)
	meta.ProtocolSummary = protos

	logs, err := db.GetLogsBySession(c.Request.Context(), sessionID, limit, offset)
	if err != nil {
		serverError(c, err)
		return
	}
	total, _ := db.GetLogCountBySession(c.Request.Context(), sessionID)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    logs,
		"total":   total,
		"meta":    meta,
	})
}

// DeleteSession removes every log row belonging to a session. The session
// itself is a grouping of api_logs by session_id, so deleting the session
// is implemented as a cascade delete on that column.
func DeleteSession(c *gin.Context) {
	middleware.RequireAdmin(c)
	if c.IsAborted() {
		return
	}
	sessionID := c.Param("id")
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "session id required"})
		return
	}
	deleted, err := db.DeleteLogsBySession(c.Request.Context(), sessionID)
	if err != nil {
		if err == db.ErrNotFound {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Session not found"})
			return
		}
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "已删除会话",
		"data":    gin.H{"deleted_logs": deleted},
	})
}

func ListStatusCodes(c *gin.Context) {
	codes, err := db.GetDistinctStatusCodes(c.Request.Context())
	if err != nil {
		serverError(c, err)
		return
	}
	if codes == nil {
		codes = []int{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": codes})
}

func TodayStats(c *gin.Context) {
	userID := 0
	if middleware.GetUserRole(c) > 2 {
		userID = c.GetInt(middleware.CtxUserID)
	}
	s, err := db.GetTodayStats(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to get stats"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": s})
}

func DailyTokenStats(c *gin.Context) {
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")
	if startDate == "" {
		startDate = time.Now().AddDate(0, 0, -6).Format("2006-01-02")
	}
	if endDate == "" {
		endDate = time.Now().Format("2006-01-02")
	}

	userID := 0
	if middleware.GetUserRole(c) > 2 {
		userID = c.GetInt(middleware.CtxUserID)
	}

	single := startDate == endDate

	if single {
		hourly, err := db.GetHourlyTokenStats(c.Request.Context(), startDate, userID)
		if err != nil {
			serverError(c, err)
			return
		}
		models, err := db.GetModelTokenStats(c.Request.Context(), startDate, endDate, userID)
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"hourly":       hourly,
				"daily":        []any{},
				"models":       models,
				"is_single_day": true,
			},
		})
		return
	}

	daily, err := db.GetDailyTokenStats(c.Request.Context(), startDate, endDate, userID)
	if err != nil {
		serverError(c, err)
		return
	}
	models, err := db.GetModelTokenStats(c.Request.Context(), startDate, endDate, userID)
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"hourly":       []any{},
			"daily":        daily,
			"models":       models,
			"is_single_day": false,
		},
	})
}

// ---------------------------------------------------------------------------
// Team CRUD
// ---------------------------------------------------------------------------

func ListTeams(c *gin.Context) {
	middleware.RequireAdmin(c)
	if c.IsAborted() {
		return
	}
	xs, err := db.GetTeams(c.Request.Context())
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, xs)
}

func GetTeam(c *gin.Context) {
	middleware.RequireAdmin(c)
	if c.IsAborted() {
		return
	}
	id, _ := strconv.Atoi(c.Param("id"))
	t, err := db.GetTeam(c.Request.Context(), id)
	if errors.Is(err, db.ErrNotFound) || t == nil {
		notFound(c, "团队不存在")
		return
	}
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, t)
}

func CreateTeam(c *gin.Context) {
	middleware.RequireRoot(c)
	if c.IsAborted() {
		return
	}
	var in db.CreateTeamInput
	if !bindJSON(c, &in) {
		return
	}
	t, err := db.CreateTeam(c.Request.Context(), in)
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, t)
}

func UpdateTeam(c *gin.Context) {
	middleware.RequireRoot(c)
	if c.IsAborted() {
		return
	}
	id, _ := strconv.Atoi(c.Param("id"))
	var in db.UpdateTeamInput
	if !bindJSON(c, &in) {
		return
	}
	t, err := db.UpdateTeam(c.Request.Context(), id, in)
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, t)
}

func DeleteTeam(c *gin.Context) {
	middleware.RequireRoot(c)
	if c.IsAborted() {
		return
	}
	id, _ := strconv.Atoi(c.Param("id"))
	if err := db.DeleteTeam(c.Request.Context(), id); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func UpdateUserTeam(c *gin.Context) {
	middleware.RequireAdmin(c)
	if c.IsAborted() {
		return
	}
	userID, _ := strconv.Atoi(c.Param("user_id"))
	currentRole := middleware.GetUserRole(c)
	// 不能操作比自己权限高的用户
	if currentRole != 1 {
		target, err := db.GetUserByID(c.Request.Context(), userID)
		if err != nil || target == nil {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "用户不存在"})
			return
		}
		if target.Role < currentRole {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "不能越级操作"})
			return
		}
	}
	var in struct {
		TeamID *int `json:"team_id"`
	}
	if !bindJSON(c, &in) {
		return
	}
	// role=2（管理员）只能将用户分配到自己所在的团队
	if currentRole == 2 {
		adminTeamID := middleware.GetTeamID(c)
		if adminTeamID == nil {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "你尚未被分配到任何团队"})
			return
		}
		if in.TeamID != nil && *in.TeamID != *adminTeamID {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "只能将用户分配到自己所在的团队"})
			return
		}
	}
	if err := db.UpdateUserTeam(c.Request.Context(), userID, in.TeamID); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func ok(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

func notFound(c *gin.Context, msg string) {
	c.JSON(http.StatusNotFound, gin.H{"success": false, "message": msg})
}

func serverError(c *gin.Context, err error) {
	slog.Error("handler error", "err", err, "path", c.Request.URL.Path)
	c.JSON(http.StatusInternalServerError, gin.H{
		"success": false,
		"message": "internal error: " + err.Error(),
	})
}

func bindJSON(c *gin.Context, dst any) bool {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil || len(body) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Request body must be JSON"})
		return false
	}
	if err := json.Unmarshal(body, dst); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Request body must be JSON"})
		return false
	}
	return true
}

// sanitizeLog was removed; the model now uses json.RawMessage directly.
