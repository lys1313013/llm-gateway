package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/lys1313013/llm-gateway/backend/internal/auth"
	"github.com/lys1313013/llm-gateway/backend/internal/db"
	"github.com/lys1313013/llm-gateway/backend/internal/middleware"
)

// ---------------------------------------------------------------------------
// Public endpoints (no JWT required)
// ---------------------------------------------------------------------------

func Register(c *gin.Context) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !bindJSON(c, &in) {
		return
	}
	in.Username = strings.TrimSpace(in.Username)

	if in.Username == "" || in.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "用户名和密码不能为空"})
		return
	}
	if len(in.Username) < 3 || len(in.Username) > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "用户名长度需在 3-100 之间"})
		return
	}
	if len(in.Password) < 6 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "密码长度至少 6 位"})
		return
	}
	if existing, _ := db.GetUserByUsername(c.Request.Context(), in.Username); existing != nil {
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": "用户名已存在"})
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		serverError(c, err)
		return
	}
	user, err := db.CreateUser(c.Request.Context(), in.Username, hash)
	if err != nil {
		serverError(c, err)
		return
	}
	token, err := auth.GenerateJWT(user.ID, user.Username)
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data": gin.H{
			"user":  gin.H{"id": user.ID, "username": user.Username},
			"token": token,
		},
	})
}

func Login(c *gin.Context) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !bindJSON(c, &in) {
		return
	}
	in.Username = strings.TrimSpace(in.Username)

	if in.Username == "" || in.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "用户名和密码不能为空"})
		return
	}
	user, err := db.GetUserByUsername(c.Request.Context(), in.Username)
	if err != nil {
		serverError(c, err)
		return
	}
	if user == nil || !auth.VerifyPassword(in.Password, user.PasswordHash) {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "用户名或密码错误"})
		return
	}
	if !user.IsActive {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "账号已被禁用"})
		return
	}
	token, err := auth.GenerateJWT(user.ID, user.Username)
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"user":  gin.H{"id": user.ID, "username": user.Username},
			"token": token,
		},
	})
}

// ---------------------------------------------------------------------------
// Protected endpoints (JWT required)
// ---------------------------------------------------------------------------

func Me(c *gin.Context) {
	uid := c.GetInt(middleware.CtxUserID)
	user, err := db.GetUserByID(c.Request.Context(), uid)
	if err != nil {
		serverError(c, err)
		return
	}
	if user == nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "用户不存在"})
		return
	}
	ok(c, user)
}

func ChangePassword(c *gin.Context) {
	uid := c.GetInt(middleware.CtxUserID)
	var in struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if !bindJSON(c, &in) {
		return
	}
	if in.OldPassword == "" || in.NewPassword == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "旧密码和新密码不能为空"})
		return
	}
	if len(in.NewPassword) < 6 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "新密码长度至少 6 位"})
		return
	}
	user, err := db.GetUserByID(c.Request.Context(), uid)
	if err != nil {
		serverError(c, err)
		return
	}
	if user == nil || !auth.VerifyPassword(in.OldPassword, user.PasswordHash) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "用户不存在或旧密码错误"})
		return
	}
	newHash, err := auth.HashPassword(in.NewPassword)
	if err != nil {
		serverError(c, err)
		return
	}
	if err := db.UpdateUserPassword(c.Request.Context(), uid, newHash); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "密码修改成功"})
}

func ListUsers(c *gin.Context) {
	xs, err := db.GetUsers(c.Request.Context())
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, xs)
}

func RemoveUser(c *gin.Context) {
	uid := c.GetInt(middleware.CtxUserID)
	id, _ := parseIntParam(c, "user_id")
	if id == uid {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "不能删除自己"})
		return
	}
	if err := db.DeleteUser(c.Request.Context(), id); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "用户已删除"})
}

// ---------------------------------------------------------------------------
// API Key management
// ---------------------------------------------------------------------------

func ListAPIKeys(c *gin.Context) {
	uid := c.GetInt(middleware.CtxUserID)
	keys, err := db.GetAPIKeysByUser(c.Request.Context(), uid)
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, keys)
}

func CreateAPIKey(c *gin.Context) {
	uid := c.GetInt(middleware.CtxUserID)
	var in struct {
		Name string `json:"name"`
	}
	_ = bindJSON(c, &in) // body optional
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "default"
	}
	if len(name) > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "名称长度不能超过 100"})
		return
	}

	fullKey, hash, prefix := auth.GenerateAPIKey()
	rec, err := db.CreateAPIKey(c.Request.Context(), db.CreateAPIKeyInput{
		UserID:    uid,
		KeyHash:   hash,
		KeyPrefix: prefix,
		KeyValue:  &fullKey,
		Name:      name,
	})
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data": gin.H{
			"id":          rec.ID,
			"user_id":     rec.UserID,
			"name":        rec.Name,
			"key_prefix":  rec.KeyPrefix,
			"is_active":   rec.IsActive,
			"created_at":  rec.CreatedAt,
			"last_used_at": rec.LastUsedAt,
			"key":         fullKey,
		},
	})
}

func DeleteAPIKey(c *gin.Context) {
	uid := c.GetInt(middleware.CtxUserID)
	id, _ := parseIntParam(c, "key_id")
	// Verify ownership before delete
	keys, _ := db.GetAPIKeysByUser(c.Request.Context(), uid)
	owned := false
	for _, k := range keys {
		if k.ID == id {
			owned = true
			break
		}
	}
	if !owned {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "无权操作该 API Key"})
		return
	}
	if err := db.DeleteAPIKey(c.Request.Context(), id); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "API Key 已删除"})
}

func ToggleAPIKey(c *gin.Context) {
	uid := c.GetInt(middleware.CtxUserID)
	id, _ := parseIntParam(c, "key_id")
	var in struct {
		IsActive bool `json:"is_active"`
	}
	if !bindJSON(c, &in) {
		return
	}
	keys, _ := db.GetAPIKeysByUser(c.Request.Context(), uid)
	owned := false
	for _, k := range keys {
		if k.ID == id {
			owned = true
			break
		}
	}
	if !owned {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "无权操作该 API Key"})
		return
	}
	rec, err := db.ToggleAPIKey(c.Request.Context(), id, in.IsActive)
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, rec)
}

func UpdateAPIKey(c *gin.Context) {
	uid := c.GetInt(middleware.CtxUserID)
	id, _ := parseIntParam(c, "key_id")
	var in struct {
		Name string `json:"name"`
	}
	if !bindJSON(c, &in) {
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "名称不能为空"})
		return
	}
	if len(name) > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "名称长度不能超过 100"})
		return
	}
	keys, _ := db.GetAPIKeysByUser(c.Request.Context(), uid)
	owned := false
	for _, k := range keys {
		if k.ID == id {
			owned = true
			break
		}
	}
	if !owned {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "无权操作该 API Key"})
		return
	}
	rec, err := db.UpdateAPIKeyName(c.Request.Context(), id, name)
	if err != nil {
		serverError(c, err)
		return
	}
	ok(c, rec)
}

// ---------------------------------------------------------------------------
// Admin test endpoints — proxy through using JWT auth (no API key needed)
// ---------------------------------------------------------------------------

// TestChat, TestMessages mirror /api/test/chat and /api/test/messages
// from the Python backend. They re-use the same proxy plumbing as the
// gateway endpoints, but authenticate with JWT instead of an API key.

// (moved to test.go to keep auth.go focused)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func parseIntParam(c *gin.Context, name string) (int, error) {
	raw := c.Param(name)
	if raw == "" {
		return 0, errors.New("missing param")
	}
	var n int
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return 0, errors.New("not a number")
		}
		n = n*10 + int(ch-'0')
	}
	return n, nil
}
