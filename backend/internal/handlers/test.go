package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"

	"github.com/gin-gonic/gin"

	"github.com/lys1313013/llm-gateway/backend/internal/db"
	"github.com/lys1313013/llm-gateway/backend/internal/models"
	"github.com/lys1313013/llm-gateway/backend/internal/proxy"
)

// TestChat is the admin-only OpenAI test endpoint (/api/test/chat).
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
	model, _ := data["model"].(string)
	routes, err := db.GetActiveRoutes(c.Request.Context())
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
		if ok, _ := path.Match(r.ModelPattern, model); ok {
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

// TestMessages is the admin-only Anthropic test endpoint (/api/test/messages).
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
	model, _ := data["model"].(string)
	routes, err := db.GetActiveRoutes(c.Request.Context())
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
		if ok, _ := path.Match(r.ModelPattern, model); ok {
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
