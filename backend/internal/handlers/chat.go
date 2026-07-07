// Package handlers contains the gin.HandlerFunc implementations for every
// route exposed by the gateway.
package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/lys1313013/llm-gateway/backend/internal/config"
	"github.com/lys1313013/llm-gateway/backend/internal/db"
	hdrpkg "github.com/lys1313013/llm-gateway/backend/internal/headers"
	"github.com/lys1313013/llm-gateway/backend/internal/middleware"
	gatewaymodels "github.com/lys1313013/llm-gateway/backend/internal/models"
	"github.com/lys1313013/llm-gateway/backend/internal/proxy"
)

// resolveSessionID 按配置顺序扫描 SESSION_ID_HEADERS，返回第一个非空值。
// 集中到一处，方便部署方同时识别多种上游客户端（例如 Claude Code 的
// X-Claude-Code-Session-Id 以及自定义 Agent 的 X-Agent-Session-Id）。
func resolveSessionID(c *gin.Context) string {
	for _, name := range config.Get().SessionIDHeaders {
		if v := strings.TrimSpace(c.GetHeader(name)); v != "" {
			return v
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// /v1/models
// ---------------------------------------------------------------------------

func ListModels(c *gin.Context) {
	var modelRecords []gatewaymodels.ExposedModel
	var err error
	if middleware.GetUserRole(c) == 1 {
		modelRecords, err = db.GetActiveExposedModels(c.Request.Context())
	} else {
		teamID := middleware.GetTeamID(c)
		modelRecords, err = db.GetActiveExposedModelsForTeam(c.Request.Context(), teamID)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	data := make([]gin.H, 0, len(modelRecords))
	for _, m := range modelRecords {
		data = append(data, gin.H{
			"id":      m.ModelID,
			"object":  "model",
			"created": m.CreateTime.Unix(),
			"owned_by": m.OwnedBy,
		})
	}
	c.JSON(http.StatusOK, gin.H{"object": "list", "data": data})
}

// ---------------------------------------------------------------------------
// /v1/chat/completions  (OpenAI)
// ---------------------------------------------------------------------------

func ChatCompletions(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Request body must be JSON", "type": "invalid_request_error",
		}})
		return
	}
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil || data == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Request body must be JSON", "type": "invalid_request_error",
		}})
		return
	}

	model, _ := data["model"].(string)

	if slog.Default().Enabled(c.Request.Context(), slog.LevelInfo) {
		headers := collectHeaders(c.Request.Header)
		slog.Info("chat: received", "headers", headers, "body", string(body))
	}

	route := matchOpenAIRoute(c, model)
	if route == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{
			"message": fmt.Sprintf("No route matched for model '%s'", model),
			"type":    "invalid_request_error",
		}})
		return
	}

	target := buildOpenAITargetURL(route.OpenAIBaseURL)

	cfg := gatewaymodels.ProxyConfig{
		TargetURL:      target,
		APIKey:         strDeref(route.APIKey),
		Timeout:        route.Timeout,
		LogRequests:    route.LogRequests,
		LogResponses:   route.LogResponses,
		Model:          strDeref(route.TargetModel, model),
		Protocol:       "openai",
		ProviderID:     route.ProviderID,
		ProviderName:   strDeref(route.ProviderName),
		RequestHeaders: hdrpkg.FromMap(collectHeaders(c.Request.Header)),
		SessionID:      resolveSessionID(c),
		UserID:         c.GetInt(middleware.CtxUserID),
	}

	status, headers, bodyRC, isStream, err := proxy.HandleOpenAI(c.Request.Context(), body, cfg)
	if err != nil {
		slog.Error("chat proxy", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"message": err.Error(), "type": "internal_server_error",
		}})
		return
	}

	if isStream {
		writeStream(c, status, headers, bodyRC)
		return
	}

	// Non-streaming: read body, return as JSON
	if bodyRC == nil {
		c.JSON(status, gin.H{"error": gin.H{
			"message": "upstream error", "type": "api_error",
		}})
		return
	}
	defer bodyRC.Close()
	respBody, _ := io.ReadAll(bodyRC)
	if status != http.StatusOK {
		// Try to forward the upstream error JSON
		var parsed any
		if json.Unmarshal(respBody, &parsed) == nil {
			c.Data(status, "application/json", respBody)
			return
		}
	}
	c.Data(status, "application/json", respBody)
}

func writeStream(c *gin.Context, status int, headers http.Header, body io.ReadCloser) {
	defer body.Close()
	h := c.Writer.Header()
	for k, v := range headers {
		// Don't forward hop-by-hop headers
		if strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Connection") {
			continue
		}
		for _, vv := range v {
			h.Add(k, vv)
		}
	}
	if h.Get("Content-Type") == "" {
		h.Set("Content-Type", "text/event-stream")
	}
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(status)
	flusher, _ := c.Writer.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			if _, werr := c.Writer.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				slog.Error("stream read", "err", err)
			}
			return
		}
	}
}

func matchOpenAIRoute(c *gin.Context, model string) *gatewaymodels.ModelRoute {
	routes, err := db.GetActiveRoutes(c.Request.Context())
	if err != nil {
		slog.Error("get active routes", "err", err)
		return nil
	}
	for i := range routes {
		r := &routes[i]
		if r.OpenAIBaseURL == nil || *r.OpenAIBaseURL == "" {
			continue
		}
		matched := matchModel(r.ModelPattern, model)
		if matched {
			return r
		}
	}
	return nil
}

func buildOpenAITargetURL(base *string) string {
	b := strings.TrimRight(strDeref(base), "/")
	if strings.HasSuffix(b, "/chat/completions") {
		return b
	}
	return b + "/chat/completions"
}

func strDeref(p *string, def ...string) string {
	if p != nil {
		return *p
	}
	if len(def) > 0 {
		return def[0]
	}
	return ""
}

// matchModel reports whether model matches the given glob pattern.
// Unlike path.Match, it treats '/' as an ordinary character so that
// "tencent*" correctly matches "tencent/hy3:free".
func matchModel(pattern, model string) bool {
	// Convert glob to regex: escape everything then replace \* with .* and \? with .
	escaped := regexp.QuoteMeta(pattern)
	escaped = strings.ReplaceAll(escaped, `\*`, ".*")
	escaped = strings.ReplaceAll(escaped, `\?`, ".")
	re, err := regexp.Compile("^" + escaped + "$")
	if err != nil {
		return false
	}
	return re.MatchString(model)
}

func collectHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if len(v) == 0 {
			continue
		}
		out[k] = strings.Join(v, ", ")
	}
	return out
}

// ---------------------------------------------------------------------------
// /v1/messages  (Anthropic)
// ---------------------------------------------------------------------------

func AnthropicMessages(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"type":  "error",
			"error": gin.H{"type": "invalid_request_error", "message": "Request body must be JSON"},
		})
		return
	}
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil || data == nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"type":  "error",
			"error": gin.H{"type": "invalid_request_error", "message": "Request body must be JSON"},
		})
		return
	}
	model, _ := data["model"].(string)

	route := matchAnthropicRoute(c, model)
	if route == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"type":  "error",
			"error": gin.H{"type": "not_found_error", "message": fmt.Sprintf("No route matched for model '%s'", model)},
		})
		return
	}

	base := strings.TrimRight(strDeref(route.AnthropicBaseURL), "/")
	target := base + "/v1/messages"

	cfg := gatewaymodels.ProxyConfig{
		TargetURL:        target,
		APIKey:           strDeref(route.APIKey),
		Timeout:          route.Timeout,
		LogRequests:      route.LogRequests,
		LogResponses:     route.LogResponses,
		Model:            strDeref(route.TargetModel, model),
		Protocol:         "anthropic",
		AnthropicVersion: defaultStr(c.GetHeader("anthropic-version"), "2023-06-01"),
		ProviderID:       route.ProviderID,
		ProviderName:     strDeref(route.ProviderName),
		RequestHeaders:   hdrpkg.FromMap(collectHeaders(c.Request.Header)),
		SessionID:        resolveSessionID(c),
		UserID:           c.GetInt(middleware.CtxUserID),
	}

	status, headers, bodyRC, isStream, err := proxy.HandleAnthropic(c.Request.Context(), body, cfg)
	if err != nil {
		slog.Error("anthropic proxy", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"type":  "error",
			"error": gin.H{"type": "internal_server_error", "message": err.Error()},
		})
		return
	}

	if isStream {
		writeStream(c, status, headers, bodyRC)
		return
	}
	if bodyRC == nil {
		c.JSON(status, gin.H{
			"type":  "error",
			"error": gin.H{"type": "api_error", "message": "upstream error"},
		})
		return
	}
	defer bodyRC.Close()
	respBody, _ := io.ReadAll(bodyRC)
	c.Data(status, "application/json", respBody)
}

func matchAnthropicRoute(c *gin.Context, model string) *gatewaymodels.ModelRoute {
	routes, err := db.GetActiveRoutes(c.Request.Context())
	if err != nil {
		slog.Error("get active routes", "err", err)
		return nil
	}
	for i := range routes {
		r := &routes[i]
		if r.AnthropicBaseURL == nil || *r.AnthropicBaseURL == "" {
			continue
		}
		matched := matchModel(r.ModelPattern, model)
		if matched {
			return r
		}
	}
	return nil
}

// strconv import kept for utility helpers
func defaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
