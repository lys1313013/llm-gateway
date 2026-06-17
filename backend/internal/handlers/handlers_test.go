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

	"github.com/lys1313013/llm-gateway/backend/internal/config"
	"github.com/lys1313013/llm-gateway/backend/internal/db"
	"github.com/lys1313013/llm-gateway/backend/internal/handlers"
	"github.com/lys1313013/llm-gateway/backend/internal/middleware"
)

func mustEnv(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

// requireEnv returns the value of k or skips the test if k is not set.
// Use this for anything that must never fall back to a hardcoded value
// (database host, database password). Plain mustEnv keeps safe defaults
// for non-sensitive parameters like the port or user name.
func requireEnv(t *testing.T, k string) string {
	t.Helper()
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	t.Skipf("skipping: required env var %s is not set", k)
	return ""
}

func setupRouter(t *testing.T) *gin.Engine {
	t.Helper()
	// Force a deterministic env so we don't pick up the dev .env.
	// Host + password MUST come from env — never hardcoded, since this
	// file is committed. Port / name / user have safe generic defaults.
	_ = os.Setenv("DB_HOST", requireEnv(t, "TEST_DB_HOST"))
	_ = os.Setenv("DB_PORT", mustEnv("TEST_DB_PORT", "5432"))
	_ = os.Setenv("DB_NAME", mustEnv("TEST_DB_NAME", "llm_gateway"))
	_ = os.Setenv("DB_USER", mustEnv("TEST_DB_USER", "postgres"))
	_ = os.Setenv("DB_PASSWORD", requireEnv(t, "TEST_DB_PASSWORD"))
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

	r.GET("/api/sessions", handlers.ListSessions)
	r.GET("/api/sessions/:id", handlers.GetSession)

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

// TestSessions_Smoke exercises the new session endpoints end-to-end:
//  1. seed three logs with two distinct session IDs and one without
//  2. GET /api/sessions?q=... filters by prefix
//  3. GET /api/sessions/:id returns meta + logs in ASC order
//  4. GET /api/sessions (no filter) returns at least 2 rows
func TestSessions_Smoke(t *testing.T) {
	r := setupRouter(t)
	suffix := time.Now().UnixNano()
	w, body := doJSON(t, r, "POST", "/api/auth/register", map[string]string{
		"username": fmt.Sprintf("gtest_sess_%d", suffix),
		"password": "test_pw_1234",
	}, nil)
	if w.Code != 201 {
		t.Fatalf("register: %d", w.Code)
	}
	token, _ := body["data"].(map[string]any)["token"].(string)
	auth := map[string]string{"Authorization": "Bearer " + token}

	sessA := fmt.Sprintf("sess-A-%d", suffix)
	sessB := fmt.Sprintf("sess-B-%d", suffix)

	ctx := context.Background()

	// Three logs for sessA, two for sessB, one with NULL session_id.
	for i := 0; i < 3; i++ {
		if err := db.InsertLog(ctx, db.InsertLogInput{
			Model:          strPtrLocal("gpt-4o-mini"),
			IsStream:       false,
			StatusCode:     200,
			ProcessingTimeMs: 100 + i,
			PromptTokens:   intPtrLocal(10 + i),
			CompletionTokens: intPtrLocal(20 + i),
			TotalTokens:    intPtrLocal(30 + 2*i),
			Protocol:       strPtrLocal("openai"),
			SessionID:      strPtrOrNilLocal(sessA),
		}); err != nil {
			t.Fatalf("insert sessA[%d]: %v", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := db.InsertLog(ctx, db.InsertLogInput{
			Model:          strPtrLocal("claude-haiku-4-5"),
			IsStream:       true,
			StatusCode:     200,
			ProcessingTimeMs: 200 + i,
			PromptTokens:   intPtrLocal(5),
			CompletionTokens: intPtrLocal(10),
			TotalTokens:    intPtrLocal(15),
			Protocol:       strPtrLocal("anthropic"),
			SessionID:      strPtrOrNilLocal(sessB),
		}); err != nil {
			t.Fatalf("insert sessB[%d]: %v", i, err)
		}
	}
	// No session — should not appear in the list
	if err := db.InsertLog(ctx, db.InsertLogInput{
		Model:        strPtrLocal("gpt-4o-mini"),
		IsStream:     false,
		StatusCode:   200,
		Protocol:     strPtrLocal("openai"),
	}); err != nil {
		t.Fatalf("insert nosess: %v", err)
	}

	// 1. List with q=sess-A filter
	w, lbody := doJSON(t, r, "GET", "/api/sessions?q=sess-A-"+fmt.Sprint(suffix), nil, auth)
	if w.Code != 200 {
		t.Fatalf("list sessions: %d: %s", w.Code, w.Body.String())
	}
	data, _ := lbody["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("expected 1 session for sessA, got %d", len(data))
	}
	row := data[0].(map[string]any)
	if row["session_id"] != sessA {
		t.Fatalf("sessA row id mismatch: %v", row["session_id"])
	}
	if int(row["request_count"].(float64)) != 3 {
		t.Fatalf("sessA request_count: want 3, got %v", row["request_count"])
	}
	if int(row["total_tokens"].(float64)) != 30+32+34 {
		t.Fatalf("sessA total_tokens: want 96, got %v", row["total_tokens"])
	}
	models, _ := row["models"].([]any)
	if len(models) != 1 || models[0] != "gpt-4o-mini" {
		t.Fatalf("sessA models: want [gpt-4o-mini], got %v", models)
	}

	// 2. List all sessions — should include at least sessA and sessB
	w, lbody = doJSON(t, r, "GET", "/api/sessions?limit=100", nil, auth)
	if w.Code != 200 {
		t.Fatalf("list all sessions: %d", w.Code)
	}
	all, _ := lbody["data"].([]any)
	foundA, foundB := false, false
	for _, r := range all {
		m := r.(map[string]any)
		if m["session_id"] == sessA {
			foundA = true
		}
		if m["session_id"] == sessB {
			foundB = true
		}
	}
	if !foundA || !foundB {
		t.Fatalf("expected both sessA and sessB in list, got foundA=%v foundB=%v", foundA, foundB)
	}

	// 3. Detail: sessA
	w, dbody := doJSON(t, r, "GET", "/api/sessions/"+sessA, nil, auth)
	if w.Code != 200 {
		t.Fatalf("get sessA: %d: %s", w.Code, w.Body.String())
	}
	logs, _ := dbody["data"].([]any)
	if len(logs) != 3 {
		t.Fatalf("sessA logs: want 3, got %d", len(logs))
	}
	// Confirm ASC by id
	firstID := int(logs[0].(map[string]any)["id"].(float64))
	lastID := int(logs[len(logs)-1].(map[string]any)["id"].(float64))
	if firstID >= lastID {
		t.Fatalf("logs not in ASC order: %d >= %d", firstID, lastID)
	}
	meta, _ := dbody["meta"].(map[string]any)
	if int(meta["request_count"].(float64)) != 3 {
		t.Fatalf("sessA meta request_count: %v", meta["request_count"])
	}

	// 4. Detail: nonexistent session
	w, _ = doJSON(t, r, "GET", "/api/sessions/does-not-exist-"+fmt.Sprint(suffix), nil, auth)
	if w.Code != 404 {
		t.Fatalf("expected 404 for missing session, got %d", w.Code)
	}
}

// local helpers — kept private to the test file so they don't pollute the
// proxy/db package. Mirror the strPtr/intPtr/strPtrOrNil helpers used by
// the production InsertLogInput call sites.
func strPtrLocal(s string) *string       { return &s }
func strPtrOrNilLocal(s string) *string  { if s == "" { return nil }; return &s }
func intPtrLocal(n int) *int             { return &n }

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
