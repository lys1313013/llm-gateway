package handlers

import (
	"github.com/gin-gonic/gin"
	"github.com/lys1313013/llm-gateway/backend/internal/db"
	"github.com/lys1313013/llm-gateway/backend/internal/middleware"
)

// All log endpoints use this boundary, including detail, deletion and summary
// queries. Missing identity or team membership must never mean global access.
func logScope(c *gin.Context) db.LogScope {
	userID := c.GetInt(middleware.CtxUserID)
	if userID <= 0 {
		return db.LogScope{}
	}
	switch middleware.GetUserRole(c) {
	case 1:
		return db.LogScope{All: true}
	case 2:
		if teamID := middleware.GetTeamID(c); teamID != nil && *teamID > 0 {
			return db.LogScope{TeamID: *teamID}
		}
	case 3:
		return db.LogScope{UserID: userID}
	}
	return db.LogScope{}
}
