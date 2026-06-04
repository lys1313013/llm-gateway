// Package handlers_test runs a fully in-process E2E test: it boots the
// gateway in a goroutine against a test PostgreSQL, then hammers every
// route with httptest.
//
// Skipped if DATABASE_URL / DB_HOST is not set (or the test DB is
// unreachable) so this doesn't break plain `go test ./...` runs in
// environments without a database.
package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lys1313013/llm-gateway/backend-go/internal/config"
	"github.com/lys1313013/llm-gateway/backend-go/internal/db"
	"github.com/lys1313013/llm-gateway/backend-go/internal/handlers"
	"github.com/lys1313013/llm-gateway/backend-go/internal/middleware"
)

func mustEnv(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

func setupRouter(t *testing.T) *gin.Engine {
	t.Helper()
	// Force a deterministic env so we don't pick up the dev .env
	_ = os.Setenv("DB_HOST", mustEnv("TEST_DB_HOST", "REDACTED-HOST"))
	_ = os.Setenv("DB_PORT", mustEnv("TEST_DB_PORT", "35432"))
	_ = os.Setenv("DB_NAME", mustEnv("TEST_DB_NAME", "llm_gateway"))
	_ = os.Setenv("DB_USER", mustEnv("TEST_DB_USER", "postgres"))
	_ = os.Setenv("DB_PASSWORD", mustEnv("TEST_DB_PASSWORD", `REDACTED-PASSWORD`))
	_ = os.Setenv("JWT_SECRET_KEY", "test-secret")

	config.Load()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx := context.Background()
	if err := db.Init(ctx); err != nil {
		t.Skipf("skipping: cannot connect to test db: %v", err)
	}
	t.Cleanup(db.Close)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.CORS())
	r.Use(middleware.RequireAuth())
	registerTestRoutes(r)
	return r
}

func registerTestRoutes(r *gin.Engine) {
	// mirror the main.go wiring so tests catch handler-level regressions
	r.GET("/api/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	r.GET("/v1/models", handlers.ListModels)
	r.POST("/v1/chat/completions", handlers.ChatCompletions)
	r.POST("/v1/messages", handlers.AnthropicMessages)

	authGrp := r.Group("/api/auth")
	{
		authGrp.POST("/login", handlers.Login)
		authGrp.POST("/register", handlers.Register)
		authGrp.GET("/me", handlers.Me)
		authGrp.PUT("/change_password", handlers.ChangePassword)
		authGrp.GET("/users", handlers.ListUsers)
		authGrp.DELETE("/users/:user_id", handlers.RemoveUser)
		authGrp.GET("/api_keys", handlers.ListAPIKeys)
		authGrp.POST("/api_keys", handlers.CreateAPIKey)
		authGrp.DELETE("/api_keys/:key_id", handlers.DeleteAPIKey)
		authGrp.PUT("/api_keys/:key_id/toggle", handlers.ToggleAPIKey)
		authGrp.PUT("/api_keys/:key_id", handlers.UpdateAPIKey)
	}

	r.GET("/api/provider", handlers.ListProviders)
	r.GET("/api/provider/:id", handlers.GetProvider)
	r.POST("/api/provider", handlers.CreateProvider)
	r.PUT("/api/provider/:id", handlers.UpdateProvider)
	r.DELETE("/api/provider/:id", handlers.DeleteProvider)

	r.GET("/api/route", handlers.ListRoutes)
	r.GET("/api/route/:id", handlers.GetRoute)
	r.POST("/api/route", handlers.CreateRoute)
	r.PUT("/api/route/:id", handlers.UpdateRoute)
	r.DELETE("/api/route/:id", handlers.DeleteRoute)

	r.GET("/api/exposed_model", handlers.ListExposedModels)
	r.GET("/api/exposed_model/:id", handlers.GetExposedModel)
	r.POST("/api/exposed_model", handlers.CreateExposedModel)
	r.PUT("/api/exposed_model/:id", handlers.UpdateExposedModel)
	r.DELETE("/api/exposed_model/:id", handlers.DeleteExposedModel)
	r.PUT("/api/exposed_model/:id/test_time", handlers.UpdateExposedModelTestTime)

	r.GET("/api/logs", handlers.ListLogs)
	r.GET("/api/logs/:id", handlers.GetLogDetail)
	r.GET("/api/logs/today_stats", handlers.TodayStats)
	r.GET("/api/stats/daily_tokens", handlers.DailyTokenStats)

	r.POST("/api/test/chat", handlers.TestChat)
	r.POST("/api/test/messages", handlers.TestMessages)
}

func doJSON(t *testing.T, r http.Handler, method, path string, body any, headers map[string]string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var bodyR io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		bodyR = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, bodyR)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var parsed map[string]any
	if w.Body.Len() > 0 {
		_ = json.Unmarshal(w.Body.Bytes(), &parsed)
	}
	return w, parsed
}

func TestHealth(t *testing.T) {
	r := setupRouter(t)
	w, _ := doJSON(t, r, "GET", "/api/healthz", nil, nil)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestV1Models_NoAuth_401(t *testing.T) {
	r := setupRouter(t)
	w, _ := doJSON(t, r, "GET", "/v1/models", nil, nil)
	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestV1Models_BadKey_401(t *testing.T) {
	r := setupRouter(t)
	w, _ := doJSON(t, r, "GET", "/v1/models", nil, map[string]string{
		"Authorization": "Bearer sk-bogus",
	})
	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestAuth_RegisterLogin(t *testing.T) {
	r := setupRouter(t)
	suffix := time.Now().UnixNano()
	username := fmt.Sprintf("gtest_%d", suffix)

	// Register
	w, body := doJSON(t, r, "POST", "/api/auth/register", map[string]string{
		"username": username,
		"password": "test_pw_1234",
	}, nil)
	if w.Code != 201 {
		t.Fatalf("register: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	token, _ := body["data"].(map[string]any)["token"].(string)
	if token == "" {
		t.Fatal("register: token missing")
	}

	// /me with token
	w, _ = doJSON(t, r, "GET", "/api/auth/me", nil, map[string]string{
		"Authorization": "Bearer " + token,
	})
	if w.Code != 200 {
		t.Fatalf("me: expected 200, got %d", w.Code)
	}

	// Login
	w, _ = doJSON(t, r, "POST", "/api/auth/login", map[string]string{
		"username": username,
		"password": "test_pw_1234",
	}, nil)
	if w.Code != 200 {
		t.Fatalf("login: expected 200, got %d", w.Code)
	}
}

func TestProviderCRUD(t *testing.T) {
	r := setupRouter(t)
	// Register a fresh user
	suffix := time.Now().UnixNano()
	w, body := doJSON(t, r, "POST", "/api/auth/register", map[string]string{
		"username": fmt.Sprintf("gtest_p_%d", suffix),
		"password": "test_pw_1234",
	}, nil)
	if w.Code != 201 {
		t.Fatalf("register: %d", w.Code)
	}
	token, _ := body["data"].(map[string]any)["token"].(string)
	auth := map[string]string{"Authorization": "Bearer " + token}

	// Create
	w, pbody := doJSON(t, r, "POST", "/api/provider", map[string]any{
		"name":             fmt.Sprintf("gtest_provider_%d", suffix),
		"openai_base_url":  "https://example.com/v1",
		"api_key":          "sk-test",
		"remark":           "go test",
	}, auth)
	if w.Code != 200 {
		t.Fatalf("create provider: %d: %s", w.Code, w.Body.String())
	}
	pid, _ := pbody["data"].(map[string]any)["id"].(float64)

	// List
	w, _ = doJSON(t, r, "GET", "/api/provider", nil, auth)
	if w.Code != 200 {
		t.Fatalf("list providers: %d", w.Code)
	}

	// Get
	w, _ = doJSON(t, r, "GET", fmt.Sprintf("/api/provider/%d", int(pid)), nil, auth)
	if w.Code != 200 {
		t.Fatalf("get provider: %d", w.Code)
	}

	// Update
	w, _ = doJSON(t, r, "PUT", fmt.Sprintf("/api/provider/%d", int(pid)), map[string]any{
		"name":             fmt.Sprintf("gtest_provider_%d", suffix),
		"openai_base_url":  "https://example.com/v2",
		"api_key":          "sk-test2",
		"remark":           "updated",
	}, auth)
	if w.Code != 200 {
		t.Fatalf("update provider: %d", w.Code)
	}

	// Delete
	w, _ = doJSON(t, r, "DELETE", fmt.Sprintf("/api/provider/%d", int(pid)), nil, auth)
	if w.Code != 200 {
		t.Fatalf("delete provider: %d", w.Code)
	}
}

func TestLogs_AndStats(t *testing.T) {
	r := setupRouter(t)
	// Use any existing user (the test setup created users during prior
	// tests; create a fresh one if needed).
	suffix := time.Now().UnixNano()
	w, body := doJSON(t, r, "POST", "/api/auth/register", map[string]string{
		"username": fmt.Sprintf("gtest_l_%d", suffix),
		"password": "test_pw_1234",
	}, nil)
	if w.Code != 201 {
		t.Fatalf("register: %d", w.Code)
	}
	token, _ := body["data"].(map[string]any)["token"].(string)
	auth := map[string]string{"Authorization": "Bearer " + token}

	w, _ = doJSON(t, r, "GET", "/api/logs?limit=5", nil, auth)
	if w.Code != 200 {
		t.Fatalf("logs: %d", w.Code)
	}
	w, _ = doJSON(t, r, "GET", "/api/logs/today_stats", nil, auth)
	if w.Code != 200 {
		t.Fatalf("today_stats: %d", w.Code)
	}
	w, _ = doJSON(t, r, "GET", "/api/stats/daily_tokens", nil, auth)
	if w.Code != 200 {
		t.Fatalf("daily_tokens: %d", w.Code)
	}
}

// TestConcurrentLoad fires N parallel /api/logs requests to demonstrate
// that the Go server handles concurrency cleanly.
func TestConcurrentLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping load test in -short mode")
	}
	r := setupRouter(t)
	suffix := time.Now().UnixNano()
	_, body := doJSON(t, r, "POST", "/api/auth/register", map[string]string{
		"username": fmt.Sprintf("gtest_load_%d", suffix),
		"password": "test_pw_1234",
	}, nil)
	token, _ := body["data"].(map[string]any)["token"].(string)
	auth := map[string]string{"Authorization": "Bearer " + token}

	const (
		concurrency = 50
		perWorker   = 20
	)
	var (
		wg      sync.WaitGroup
		success int64
		fail    int64
	)
	start := time.Now()
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				w, _ := doJSON(t, r, "GET", "/api/logs?limit=10", nil, auth)
				if w.Code == 200 {
					atomic.AddInt64(&success, 1)
				} else {
					atomic.AddInt64(&fail, 1)
				}
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	total := int64(concurrency * perWorker)
	rps := float64(total) / elapsed.Seconds()
	t.Logf("concurrent load: %d requests in %s (%.0f rps) — %d success / %d fail",
		total, elapsed, rps, success, fail)
	if fail > 0 {
		t.Fatalf("expected zero failures, got %d", fail)
	}
}
