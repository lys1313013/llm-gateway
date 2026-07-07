package middleware

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/lys1313013/llm-gateway/backend/internal/auth"
	"github.com/lys1313013/llm-gateway/backend/internal/db"
)

const (
	CtxUserID   = "current_user_id"
	CtxUsername = "current_username"
	CtxUserRole = "current_user_role"
	CtxTeamID   = "current_team_id"
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
	c.Set(CtxUserRole, claims.Role)
	c.Set(CtxTeamID, claims.TeamID)
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
	c.Set(CtxUserRole, user.Role)
	c.Set(CtxTeamID, user.TeamID)
	c.Next()
}

func abort(c *gin.Context, status int, body any) {
	c.AbortWithStatusJSON(status, body)
}

// GetUserRole returns the role value from the Gin context. Returns 99 if not
// set (99 > Common User=3, so all role gates reject unauthenticated access).
func GetUserRole(c *gin.Context) int {
	role, _ := c.Get(CtxUserRole)
	if r, ok := role.(int); ok {
		return r
	}
	return 99
}

// RequireAdmin aborts with 403 if the current user's role is above 2 (common user).
func RequireAdmin(c *gin.Context) {
	if GetUserRole(c) > 2 {
		abort(c, http.StatusForbidden, gin.H{
			"success": false,
			"message": "权限不足：需要管理员权限",
		})
	}
}

// RequireRoot aborts with 403 if the current user's role is above 1 (non-root).
func RequireRoot(c *gin.Context) {
	if GetUserRole(c) > 1 {
		abort(c, http.StatusForbidden, gin.H{
			"success": false,
			"message": "权限不足：需要超级管理员权限",
		})
	}
}

// GetTeamID returns the team ID pointer from the Gin context. Returns nil if
// the user has no team or the value is not set.
func GetTeamID(c *gin.Context) *int {
	val, _ := c.Get(CtxTeamID)
	if v, ok := val.(*int); ok {
		return v
	}
	return nil
}
