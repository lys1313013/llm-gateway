package middleware

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/lys1313013/llm-gateway/backend-go/internal/auth"
	"github.com/lys1313013/llm-gateway/backend-go/internal/db"
)

const (
	CtxUserID   = "current_user_id"
	CtxUsername = "current_username"
)

// authWhitelist are the only paths allowed without any auth.
var authWhitelist = []string{
	"/api/auth/login",
	"/api/auth/register",
	"/api/healthz",
}

// RequireAuth is a before-request guard that enforces the right auth for
// the path family. /api/* → JWT. /v1/* → API Key. Everything else passes.
func RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// CORS preflight
		if c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}

		path := c.Request.URL.Path

		// Whitelist
		for _, w := range authWhitelist {
			if path == w || strings.HasPrefix(path, w+"/") {
				c.Next()
				return
			}
		}

		switch {
		case strings.HasPrefix(path, "/api/"):
			applyJWT(c)
		case strings.HasPrefix(path, "/v1/"):
			applyAPIKey(c)
		default:
			c.Next()
		}
	}
}

func applyJWT(c *gin.Context) {
	hdr := c.GetHeader("Authorization")
	if !strings.HasPrefix(hdr, "Bearer ") {
		abort(c, http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "未授权：缺少 Token",
		})
		return
	}
	claims, err := auth.DecodeJWT(strings.TrimPrefix(hdr, "Bearer "))
	if err != nil {
		abort(c, http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "未授权：Token 无效或已过期",
		})
		return
	}
	c.Set(CtxUserID, claims.UserID)
	c.Set(CtxUsername, claims.Username)
	c.Next()
}

func applyAPIKey(c *gin.Context) {
	apiKey := c.GetHeader("x-api-key")
	if apiKey == "" {
		// Also accept Authorization: Bearer sk-...
		if hdr := c.GetHeader("Authorization"); strings.HasPrefix(hdr, "Bearer ") {
			v := strings.TrimPrefix(hdr, "Bearer ")
			if strings.HasPrefix(v, "sk-") {
				apiKey = v
			}
		}
	}
	if apiKey == "" {
		abort(c, http.StatusUnauthorized, gin.H{
			"error": gin.H{
				"message": "未授权：缺少 API Key",
				"type":    "authentication_error",
			},
		})
		return
	}
	hash := auth.HashAPIKey(apiKey)
	rec, err := db.GetAPIKeyByHash(c.Request.Context(), hash)
	if err != nil {
		slog.Error("api key lookup", "err", err)
		abort(c, http.StatusInternalServerError, gin.H{
			"error": gin.H{"message": "internal error", "type": "server_error"},
		})
		return
	}
	if rec == nil {
		abort(c, http.StatusUnauthorized, gin.H{
			"error": gin.H{
				"message": "未授权：API Key 无效",
				"type":    "authentication_error",
			},
		})
		return
	}
	if !rec.IsActive {
		abort(c, http.StatusForbidden, gin.H{
			"error": gin.H{
				"message": "未授权：API Key 已被禁用",
				"type":    "authentication_error",
			},
		})
		return
	}

	// Look up the user to check if the user is active
	user, err := db.GetUserByID(c.Request.Context(), rec.UserID)
	if err != nil || user == nil || !user.IsActive {
		abort(c, http.StatusForbidden, gin.H{
			"error": gin.H{
				"message": "未授权：用户已被禁用",
				"type":    "authentication_error",
			},
		})
		return
	}

	// Fire-and-forget update of last_used_at
	go func(id int) { _ = db.UpdateAPIKeyLastUsed(c.Copy().Request.Context(), id) }(rec.ID)

	c.Set(CtxUserID, rec.UserID)
	c.Set(CtxUsername, user.Username)
	c.Next()
}

func abort(c *gin.Context, status int, body any) {
	c.AbortWithStatusJSON(status, body)
}
