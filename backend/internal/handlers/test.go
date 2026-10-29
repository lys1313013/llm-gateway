package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/lys1313013/llm-gateway/backend/internal/db"
	"github.com/lys1313013/llm-gateway/backend/internal/middleware"
	"github.com/lys1313013/llm-gateway/backend/internal/models"
	"github.com/lys1313013/llm-gateway/backend/internal/proxy"
)

// testRouteTeamID permits an explicit cross-team target only for root. Team
// admins may test their own routes before exposure; common users must pass the
// same model authorization as /v1 below.
func testRouteTeamID(c *gin.Context, data map[string]any) (int, bool) {
	role := middleware.GetUserRole(c)
	if c.GetInt(middleware.CtxUserID) <= 0 || role < 1 || role > 3 {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"type": "permission_error", "message": "Invalid caller"}})
		return 0, false
	}
	teamID := routeTeamID(c)
	if raw, present := data["team_id"]; present {
		value, valid := raw.(float64)
		if !valid || value <= 0 || value >= float64(math.MaxInt) || math.Trunc(value) != value {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "team_id must be a positive integer"}})
			return 0, false
		}
		requested := int(value)
		if role != 1 && requested != teamID {
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"type": "permission_error", "message": "Cannot test another team's routes"}})
			return 0, false
		}
		teamID = requested
	}
	if teamID <= 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"type": "permission_error", "message": "A team is required to test models"}})
		return 0, false
	}
	return teamID, true
}

func authorizeTestModel(c *gin.Context, data map[string]any, protocol string) (int, bool) {
	model, _ := data["model"].(string)
	if model == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "model is required"}})
		return 0, false
	}
	teamID, ok := testRouteTeamID(c, data)
	if !ok {
		return 0, false
	}
	if middleware.GetUserRole(c) == 3 && !requireModelAccess(c, model, protocol) {
		return 0, false
	}
	return teamID, true
}

// TestChat is the authorized OpenAI test endpoint (/api/test/chat).
// Authentication is JWT (the RequireAuth middleware already ran); we don't
// need an API key.
func TestChat(c *gin.Context) {
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
	teamID, allowed := authorizeTestModel(c, data, "openai")
	if !allowed {
		return
	}
	// team_id is gateway management metadata, not an upstream API parameter.
	delete(data, "team_id")
	body, _ = json.Marshal(data)
	model, _ := data["model"].(string)
	routes, err := db.GetActiveRoutes(c.Request.Context(), teamID)
	if err != nil {
		serverError(c, err)
		return
	}
	var route *models.ModelRoute
	for i := range routes {
		r := &routes[i]
		if r.OpenAIBaseURL == nil || *r.OpenAIBaseURL == "" {
			continue
		}
		if ok := matchModel(r.ModelPattern, model); ok {
			route = r
			break
		}
	}
	if route == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{
			"message": fmt.Sprintf("No route matched for model '%s'", model),
			"type":    "invalid_request_error",
		}})
		return
	}
	target := buildOpenAITargetURL(route.OpenAIBaseURL)
	cfg := models.ProxyConfig{
		TargetURL:    target,
		APIKey:       strDeref(route.APIKey),
		Timeout:      route.Timeout,
		LogRequests:  route.LogRequests,
		LogResponses: route.LogResponses,
		Model:        strDeref(route.TargetModel, model),
		Protocol:     "openai",
		ProviderID:   route.ProviderID,
		ProviderName: strDeref(route.ProviderName),
		UserID:       c.GetInt(middleware.CtxUserID),
	}

	status, headers, bodyRC, isStream, err := proxy.HandleOpenAI(c.Request.Context(), body, cfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"message": err.Error(), "type": "internal_server_error",
		}})
		return
	}
	if isStream {
		writeStream(c, status, headers, bodyRC)
		return
	}
	if bodyRC == nil {
		c.JSON(status, gin.H{"error": gin.H{
			"message": "upstream error", "type": "api_error",
		}})
		return
	}
	defer bodyRC.Close()
	respBody, _ := io.ReadAll(bodyRC)
	c.Data(status, "application/json", respBody)
}

// TestMessages is the authorized Anthropic test endpoint (/api/test/messages).
func TestMessages(c *gin.Context) {
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
	teamID, allowed := authorizeTestModel(c, data, "anthropic")
	if !allowed {
		return
	}
	// team_id is gateway management metadata, not an upstream API parameter.
	delete(data, "team_id")
	body, _ = json.Marshal(data)
	model, _ := data["model"].(string)
	routes, err := db.GetActiveRoutes(c.Request.Context(), teamID)
	if err != nil {
		serverError(c, err)
		return
	}
	var route *models.ModelRoute
	for i := range routes {
		r := &routes[i]
		if r.AnthropicBaseURL == nil || *r.AnthropicBaseURL == "" {
			continue
		}
		if ok := matchModel(r.ModelPattern, model); ok {
			route = r
			break
		}
	}
	if route == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"type":  "error",
			"error": gin.H{"type": "not_found_error", "message": fmt.Sprintf("No route matched for model '%s'", model)},
		})
		return
	}
	base := strDeref(route.AnthropicBaseURL)
	target := base + "/v1/messages"
	cfg := models.ProxyConfig{
		TargetURL:        target,
		APIKey:           strDeref(route.APIKey),
		Timeout:          route.Timeout,
		LogRequests:      route.LogRequests,
		LogResponses:     route.LogResponses,
		Model:            strDeref(route.TargetModel, model),
		Protocol:         "anthropic",
		AnthropicVersion: "2023-06-01",
		ProviderID:       route.ProviderID,
		ProviderName:     strDeref(route.ProviderName),
		UserID:           c.GetInt(middleware.CtxUserID),
	}

	status, headers, bodyRC, isStream, err := proxy.HandleAnthropic(c.Request.Context(), body, cfg)
	if err != nil {
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
