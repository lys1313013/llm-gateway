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

	"github.com/lys1313013/llm-gateway/backend/internal/db"
)

// ---------------------------------------------------------------------------
// Provider CRUD
// ---------------------------------------------------------------------------

func ListProviders(c *gin.Context) {
	xs, err := db.GetProviders(c.Request.Context())
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, xs)
}

func GetProvider(c *gin.Context) {
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
	id, _ := strconv.Atoi(c.Param("id"))
	if err := db.DeleteProvider(c.Request.Context(), id); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ---------------------------------------------------------------------------
// Model route CRUD
// ---------------------------------------------------------------------------

func ListRoutes(c *gin.Context) {
	xs, err := db.GetRoutes(c.Request.Context())
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, xs)
}

func GetRoute(c *gin.Context) {
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
	xs, err := db.GetExposedModels(c.Request.Context())
	if err != nil {
		serverError(c, err)
		return
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
	id, _ := strconv.Atoi(c.Param("id"))
	if err := db.DeleteExposedModel(c.Request.Context(), id); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func UpdateExposedModelTestTime(c *gin.Context) {
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
	filter := db.LogListFilter{
		Limit:      limit,
		Offset:     offset,
		Model:      model,
		Protocol:   protocol,
		StatusCode: statusCode,
	}
	logs, err := db.GetLogs(c.Request.Context(), filter)
	if err != nil {
		serverError(c, err)
		return
	}
	total, _ := db.GetLogCount(c.Request.Context(), filter.Model, filter.Protocol, filter.StatusCode)
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
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Log not found"})
		return
	}
	// json.RawMessage already serializes as JSON; pass through.
	c.JSON(http.StatusOK, gin.H{"success": true, "data": log})
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
	s, err := db.GetTodayStats(c.Request.Context())
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

	single := startDate == endDate

	if single {
		hourly, err := db.GetHourlyTokenStats(c.Request.Context(), startDate)
		if err != nil {
			serverError(c, err)
			return
		}
		models, err := db.GetModelTokenStats(c.Request.Context(), startDate, endDate)
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

	daily, err := db.GetDailyTokenStats(c.Request.Context(), startDate, endDate)
	if err != nil {
		serverError(c, err)
		return
	}
	models, err := db.GetModelTokenStats(c.Request.Context(), startDate, endDate)
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
