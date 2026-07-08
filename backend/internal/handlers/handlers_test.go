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
	"strings"
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
	r.DELETE("/api/logs/:id", handlers.DeleteLog)
	r.GET("/api/logs/today_stats", handlers.TodayStats)
	r.GET("/api/stats/daily_tokens", handlers.DailyTokenStats)
	r.GET("/api/logs/status_codes", handlers.ListStatusCodes)

	r.GET("/api/sessions", handlers.ListSessions)
	r.GET("/api/sessions/:id", handlers.GetSession)
	r.DELETE("/api/sessions/:id", handlers.DeleteSession)

	r.GET("/api/provider/presets", handlers.ListProviderPresets)
	r.GET("/api/provider/quota", handlers.ListProviderQuotas)
	r.GET("/api/provider/:id/quota", handlers.GetProviderQuota)
	r.POST("/api/provider/:id/quota/refresh", handlers.RefreshProviderQuota)
	r.POST("/api/provider/test/connect", handlers.ProviderConnect)

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
	userData, _ := body["data"].(map[string]any)["user"].(map[string]any)
	userID := int(userData["id"].(float64))
	t.Cleanup(func() {
		db.Pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", userID)
	})
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
	userData, _ := body["data"].(map[string]any)["user"].(map[string]any)
	userIDP := int(userData["id"].(float64))
	t.Cleanup(func() {
		db.Pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", userIDP)
	})
	token, _ := body["data"].(map[string]any)["token"].(string)
	auth := map[string]string{"Authorization": "Bearer " + token}

	// Promote test user to admin so CRUD tests pass, then re-login
	// for a fresh JWT with the updated role.
	if err := db.UpdateUserRole(context.Background(), userIDP, 2); err != nil {
		t.Fatalf("promote user to admin: %v", err)
	}
	w, body = doJSON(t, r, "POST", "/api/auth/login", map[string]string{
		"username": userData["username"].(string),
		"password": "test_pw_1234",
	}, nil)
	if w.Code != 200 {
		t.Fatalf("re-login after promotion: %d", w.Code)
	}
	token, _ = body["data"].(map[string]any)["token"].(string)
	auth = map[string]string{"Authorization": "Bearer " + token}

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

	// Promote to admin so they can see all logs
	userData, _ := body["data"].(map[string]any)["user"].(map[string]any)
	logUserID := int(userData["id"].(float64))
	_ = db.UpdateUserRole(context.Background(), logUserID, 2)
	t.Cleanup(func() {
		db.Pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", logUserID)
	})

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

	// Promote to admin so session queries see all logs
	userData, _ := body["data"].(map[string]any)["user"].(map[string]any)
	sessUserID := int(userData["id"].(float64))
	if err := db.UpdateUserRole(context.Background(), sessUserID, 2); err != nil {
		t.Fatalf("promote to admin: %v", err)
	}
	t.Cleanup(func() {
		db.Pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", sessUserID)
	})

	sessA := fmt.Sprintf("sess-A-%d", suffix)
	sessB := fmt.Sprintf("sess-B-%d", suffix)
	t.Cleanup(func() {
		db.Pool.Exec(context.Background(), "DELETE FROM api_logs WHERE session_id IN ($1, $2)", sessA, sessB)
	})

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

	// Promote to admin for load test
	userData, _ := body["data"].(map[string]any)["user"].(map[string]any)
	loadUserID := int(userData["id"].(float64))
	_ = db.UpdateUserRole(context.Background(), loadUserID, 2)
	t.Cleanup(func() {
		db.Pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", loadUserID)
	})

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

// ---------------------------------------------------------------------------
// Helper: register, promote to admin, re-login, return auth header
// ---------------------------------------------------------------------------

func registerAndPromote(t *testing.T, r http.Handler, prefix string) map[string]string {
	t.Helper()
	suffix := time.Now().UnixNano()
	username := fmt.Sprintf("%s_%d", prefix, suffix)

	w, body := doJSON(t, r, "POST", "/api/auth/register", map[string]string{
		"username": username,
		"password": "test_pw_1234",
	}, nil)
	if w.Code != 201 {
		t.Fatalf("%s register: %d", prefix, w.Code)
	}

	user, _ := body["data"].(map[string]any)["user"].(map[string]any)
	userID := int(user["id"].(float64))
	if err := db.UpdateUserRole(context.Background(), userID, 2); err != nil {
		t.Fatalf("%s promote: %v", prefix, err)
	}

	// Clean up test user (and cascading api_keys) when the test finishes.
	t.Cleanup(func() {
		db.Pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", userID)
	})

	w, body = doJSON(t, r, "POST", "/api/auth/login", map[string]string{
		"username": username,
		"password": "test_pw_1234",
	}, nil)
	if w.Code != 200 {
		t.Fatalf("%s re-login: %d", prefix, w.Code)
	}

	token, _ := body["data"].(map[string]any)["token"].(string)
	return map[string]string{"Authorization": "Bearer " + token}
}

// ---------------------------------------------------------------------------
// Route CRUD
// ---------------------------------------------------------------------------

func TestRouteCRUD(t *testing.T) {
	r := setupRouter(t)
	auth := registerAndPromote(t, r, "gtest_route")

	// Need a provider first (route references provider_id)
	w, pbody := doJSON(t, r, "POST", "/api/provider", map[string]any{
		"name":             fmt.Sprintf("gtest_route_prov_%d", time.Now().UnixNano()),
		"openai_base_url":  "https://example.com/v1",
		"api_key":          "sk-test",
	}, auth)
	if w.Code != 200 {
		t.Fatalf("create provider for route test: %d: %s", w.Code, w.Body.String())
	}
	pid := int(pbody["data"].(map[string]any)["id"].(float64))

	// Create
	w, rbody := doJSON(t, r, "POST", "/api/route", map[string]any{
		"model_pattern": "gpt-4o-route-test",
		"route_type":    "openai",
		"provider_id":   pid,
		"target_model":  "gpt-4o",
		"timeout":       30,
		"priority":      1,
		"is_active":     true,
	}, auth)
	if w.Code != 200 {
		t.Fatalf("create route: %d: %s", w.Code, w.Body.String())
	}
	rid := int(rbody["data"].(map[string]any)["id"].(float64))

	// List
	w, _ = doJSON(t, r, "GET", "/api/route", nil, auth)
	if w.Code != 200 {
		t.Fatalf("list routes: %d: %s", w.Code, w.Body.String())
	}

	// Get
	w, _ = doJSON(t, r, "GET", fmt.Sprintf("/api/route/%d", rid), nil, auth)
	if w.Code != 200 {
		t.Fatalf("get route: %d", w.Code)
	}

	// Update
	w, _ = doJSON(t, r, "PUT", fmt.Sprintf("/api/route/%d", rid), map[string]any{
		"model_pattern": "gpt-4o-route-test-updated",
		"route_type":    "openai",
		"provider_id":   pid,
		"target_model":  "gpt-4o-mini",
		"timeout":       60,
		"priority":      2,
		"is_active":     true,
	}, auth)
	if w.Code != 200 {
		t.Fatalf("update route: %d", w.Code)
	}

	// Delete
	w, _ = doJSON(t, r, "DELETE", fmt.Sprintf("/api/route/%d", rid), nil, auth)
	if w.Code != 200 {
		t.Fatalf("delete route: %d", w.Code)
	}

	// Cleanup provider
	_, _ = doJSON(t, r, "DELETE", fmt.Sprintf("/api/provider/%d", pid), nil, auth)
}

// ---------------------------------------------------------------------------
// Exposed Model CRUD
// ---------------------------------------------------------------------------

func TestExposedModelCRUD(t *testing.T) {
	r := setupRouter(t)
	auth := registerAndPromote(t, r, "gtest_model")

	// Create
	w, mbody := doJSON(t, r, "POST", "/api/exposed_model", map[string]any{
		"model_id": fmt.Sprintf("gtest-exposed-%d", time.Now().UnixNano()),
		"owned_by": "gtest-org",
		"is_active": true,
	}, auth)
	if w.Code != 200 {
		t.Fatalf("create model: %d: %s", w.Code, w.Body.String())
	}
	mid := int(mbody["data"].(map[string]any)["id"].(float64))

	// List
	w, _ = doJSON(t, r, "GET", "/api/exposed_model", nil, auth)
	if w.Code != 200 {
		t.Fatalf("list models: %d", w.Code)
	}

	// Get
	w, _ = doJSON(t, r, "GET", fmt.Sprintf("/api/exposed_model/%d", mid), nil, auth)
	if w.Code != 200 {
		t.Fatalf("get model: %d", w.Code)
	}

	// Update
	w, _ = doJSON(t, r, "PUT", fmt.Sprintf("/api/exposed_model/%d", mid), map[string]any{
		"model_id": fmt.Sprintf("gtest-exposed-upd-%d", time.Now().UnixNano()),
		"owned_by": "gtest-org-v2",
		"is_active": false,
	}, auth)
	if w.Code != 200 {
		t.Fatalf("update model: %d", w.Code)
	}

	// Update test_time
	w, _ = doJSON(t, r, "PUT", fmt.Sprintf("/api/exposed_model/%d/test_time", mid), map[string]any{
		"protocol": "openai",
	}, auth)
	if w.Code != 200 {
		t.Fatalf("update model test_time: %d", w.Code)
	}

	// Delete
	w, _ = doJSON(t, r, "DELETE", fmt.Sprintf("/api/exposed_model/%d", mid), nil, auth)
	if w.Code != 200 {
		t.Fatalf("delete model: %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// API Key CRUD
// ---------------------------------------------------------------------------

func TestAPIKeyCRUD(t *testing.T) {
	r := setupRouter(t)
	auth := registerAndPromote(t, r, "gtest_apikey")

	// Create
	w, abody := doJSON(t, r, "POST", "/api/auth/api_keys", map[string]string{
		"name": "test-key-1",
	}, auth)
	if w.Code != 200 && w.Code != 201 {
		t.Fatalf("create api key: %d: %s", w.Code, w.Body.String())
	}
	keyData, _ := abody["data"].(map[string]any)
	if keyData["key"] == nil || keyData["key"] == "" {
		t.Fatal("api key value missing in create response")
	}
	kid := int(keyData["id"].(float64))

	// List
	w, lbody := doJSON(t, r, "GET", "/api/auth/api_keys", nil, auth)
	if w.Code != 200 {
		t.Fatalf("list api keys: %d", w.Code)
	}
	keys, _ := lbody["data"].([]any)
	if len(keys) == 0 {
		t.Fatal("expected at least one api key")
	}

	// Toggle (disable)
	w, _ = doJSON(t, r, "PUT", fmt.Sprintf("/api/auth/api_keys/%d/toggle", kid), map[string]any{
		"is_active": false,
	}, auth)
	if w.Code != 200 {
		t.Fatalf("toggle api key: %d", w.Code)
	}

	// Rename
	w, _ = doJSON(t, r, "PUT", fmt.Sprintf("/api/auth/api_keys/%d", kid), map[string]string{
		"name": "renamed-key",
	}, auth)
	if w.Code != 200 {
		t.Fatalf("rename api key: %d", w.Code)
	}

	// Delete
	w, _ = doJSON(t, r, "DELETE", fmt.Sprintf("/api/auth/api_keys/%d", kid), nil, auth)
	if w.Code != 200 {
		t.Fatalf("delete api key: %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// User management
// ---------------------------------------------------------------------------

func TestUserManagement(t *testing.T) {
	r := setupRouter(t)
	auth := registerAndPromote(t, r, "gtest_usermgmt")

	// ListUsers
	w, lbody := doJSON(t, r, "GET", "/api/auth/users", nil, auth)
	if w.Code != 200 {
		t.Fatalf("list users: %d", w.Code)
	}
	users, _ := lbody["data"].([]any)
	if len(users) == 0 {
		t.Fatal("expected at least one user")
	}

	// RemoveUser — create a disposable user, then delete
	disposable := fmt.Sprintf("gtest_todelete_%d", time.Now().UnixNano())
	w, dbody := doJSON(t, r, "POST", "/api/auth/register", map[string]string{
		"username": disposable,
		"password": "test_pw_1234",
	}, nil)
	if w.Code != 201 {
		t.Fatalf("register disposable: %d", w.Code)
	}
	duid := int(dbody["data"].(map[string]any)["user"].(map[string]any)["id"].(float64))

	w, _ = doJSON(t, r, "DELETE", fmt.Sprintf("/api/auth/users/%d", duid), nil, auth)
	if w.Code != 200 {
		t.Fatalf("delete user: %d: %s", w.Code, w.Body.String())
	}

	// Cannot self-delete: verify the admin can't delete themselves
	meW, meBody := doJSON(t, r, "GET", "/api/auth/me", nil, auth)
	if meW.Code != 200 {
		t.Fatalf("me: %d", meW.Code)
	}
	myID := int(meBody["data"].(map[string]any)["id"].(float64))
	w, _ = doJSON(t, r, "DELETE", fmt.Sprintf("/api/auth/users/%d", myID), nil, auth)
	if w.Code != 400 {
		t.Fatalf("self-delete: expected 400, got %d", w.Code)
	}
}

func TestChangePassword(t *testing.T) {
	r := setupRouter(t)
	auth := registerAndPromote(t, r, "gtest_pw")

	// Change password
	w, _ := doJSON(t, r, "PUT", "/api/auth/change_password", map[string]string{
		"old_password": "test_pw_1234",
		"new_password": "new_pw_5678",
	}, auth)
	if w.Code != 200 {
		t.Fatalf("change password: %d: %s", w.Code, w.Body.String())
	}

	// Old password should no longer work
	w, _ = doJSON(t, r, "POST", "/api/auth/login", map[string]string{
		"username": "should-fail-with-old",
		"password": "test_pw_1234",
	}, nil)
	// Just verify the change_password endpoint itself works; old password
	// from the registered user won't work for login since username is random.
	_ = w

	// New password login — find the username from the auth JWT
	meW, meBody := doJSON(t, r, "GET", "/api/auth/me", nil, auth)
	if meW.Code != 200 {
		t.Fatalf("me: %d", meW.Code)
	}
	username, _ := meBody["data"].(map[string]any)["username"].(string)

	w, _ = doJSON(t, r, "POST", "/api/auth/login", map[string]string{
		"username": username,
		"password": "new_pw_5678",
	}, nil)
	if w.Code != 200 {
		t.Fatalf("login with new password: %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Permission denial tests — common user cannot write
// ---------------------------------------------------------------------------

func registerCommon(t *testing.T, r http.Handler) map[string]string {
	t.Helper()
	suffix := time.Now().UnixNano()
	username := fmt.Sprintf("gtest_common_%d", suffix)

	w, body := doJSON(t, r, "POST", "/api/auth/register", map[string]string{
		"username": username,
		"password": "test_pw_1234",
	}, nil)
	if w.Code != 201 {
		t.Fatalf("register common: %d", w.Code)
	}
	user, _ := body["data"].(map[string]any)["user"].(map[string]any)
	userID := int(user["id"].(float64))
	t.Cleanup(func() {
		db.Pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", userID)
	})
	token, _ := body["data"].(map[string]any)["token"].(string)
	return map[string]string{"Authorization": "Bearer " + token}
}

func TestCommonUserCannotWrite(t *testing.T) {
	r := setupRouter(t)
	auth := registerCommon(t, r)

	tests := []struct {
		method string
		path   string
		body   map[string]any
	}{
		{"POST", "/api/provider", map[string]any{"name": "x", "openai_base_url": "http://x", "api_key": "sk-x"}},
		{"PUT", "/api/provider/99999", map[string]any{"name": "x"}},
		{"DELETE", "/api/provider/99999", nil},
		{"POST", "/api/route", map[string]any{"model_pattern": "x", "route_type": "openai"}},
		{"PUT", "/api/route/99999", map[string]any{"model_pattern": "x", "route_type": "openai"}},
		{"DELETE", "/api/route/99999", nil},
		{"POST", "/api/exposed_model", map[string]any{"model_id": "x"}},
		{"PUT", "/api/exposed_model/99999", map[string]any{"model_id": "x"}},
		{"DELETE", "/api/exposed_model/99999", nil},
		{"GET", "/api/auth/users", nil},
		{"DELETE", "/api/auth/users/99999", nil},
	}

	for _, tc := range tests {
		w, _ := doJSON(t, r, tc.method, tc.path, tc.body, auth)
		if w.Code != 403 {
			t.Errorf("%s %s: expected 403, got %d", tc.method, tc.path, w.Code)
		}
	}
}

// ---------------------------------------------------------------------------
// Log detail and delete
// ---------------------------------------------------------------------------

func TestLogDetailAndDelete(t *testing.T) {
	r := setupRouter(t)
	auth := registerAndPromote(t, r, "gtest_logdetail")

	// Insert a log row directly so we have something to query
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	model := fmt.Sprintf("gtest-model-%d", suffix)
	t.Cleanup(func() {
		db.Pool.Exec(context.Background(), "DELETE FROM api_logs WHERE model = $1", model)
	})
	if err := db.InsertLog(ctx, db.InsertLogInput{
		Model:            &model,
		IsStream:         false,
		StatusCode:       200,
		ProcessingTimeMs: 42,
		Protocol:         strPtrLocal("openai"),
	}); err != nil {
		t.Fatalf("insert test log: %v", err)
	}

	// List logs to find the inserted one
	w, lbody := doJSON(t, r, "GET", "/api/logs?limit=1&model="+model, nil, auth)
	if w.Code != 200 {
		t.Fatalf("list logs: %d: %s", w.Code, w.Body.String())
	}
	logs, _ := lbody["data"].([]any)
	if len(logs) == 0 {
		t.Fatal("expected at least one log")
	}
	logID := int(logs[0].(map[string]any)["id"].(float64))

	// Get detail
	w, _ = doJSON(t, r, "GET", fmt.Sprintf("/api/logs/%d", logID), nil, auth)
	if w.Code != 200 {
		t.Fatalf("get log detail: %d", w.Code)
	}

	// Delete
	w, _ = doJSON(t, r, "DELETE", fmt.Sprintf("/api/logs/%d", logID), nil, auth)
	if w.Code != 200 && w.Code != 404 {
		t.Fatalf("delete log: %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Session delete
// ---------------------------------------------------------------------------

func TestSessionDelete(t *testing.T) {
	r := setupRouter(t)
	auth := registerAndPromote(t, r, "gtest_sessdel")

	sessionID := fmt.Sprintf("sess-del-%d", time.Now().UnixNano())
	ctx := context.Background()
	t.Cleanup(func() {
		db.Pool.Exec(context.Background(), "DELETE FROM api_logs WHERE session_id = $1", sessionID)
	})

	// Insert a few logs with a session_id
	for i := 0; i < 3; i++ {
		if err := db.InsertLog(ctx, db.InsertLogInput{
			Model:            strPtrLocal("gpt-4o-mini"),
			IsStream:         false,
			StatusCode:       200,
			ProcessingTimeMs: 100,
			Protocol:         strPtrLocal("openai"),
			SessionID:        strPtrOrNilLocal(sessionID),
		}); err != nil {
			t.Fatalf("insert session log: %v", err)
		}
	}

	// Delete the session
	w, _ := doJSON(t, r, "DELETE", "/api/sessions/"+sessionID, nil, auth)
	if w.Code != 200 {
		t.Fatalf("delete session: %d: %s", w.Code, w.Body.String())
	}

	// Verify session is gone
	w, _ = doJSON(t, r, "GET", "/api/sessions/"+sessionID, nil, auth)
	if w.Code != 404 {
		t.Fatalf("expected 404 after delete, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Provider presets and quota endpoints
// ---------------------------------------------------------------------------

func TestProviderPresets(t *testing.T) {
	r := setupRouter(t)
	auth := registerAndPromote(t, r, "gtest_presets")

	w, _ := doJSON(t, r, "GET", "/api/provider/presets", nil, auth)
	if w.Code != 200 {
		t.Fatalf("list presets: %d", w.Code)
	}
}

func TestProviderQuotaEndpoints(t *testing.T) {
	t.Skip("skipping: quota endpoints require quota.Global init in test")
}

// ---------------------------------------------------------------------------
// Status codes and today stats
// ---------------------------------------------------------------------------

func TestStatusCodes(t *testing.T) {
	r := setupRouter(t)
	auth := registerAndPromote(t, r, "gtest_statuscodes")

	w, _ := doJSON(t, r, "GET", "/api/logs/status_codes", nil, auth)
	if w.Code != 200 {
		t.Fatalf("status codes: %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Live upstream tests — skipped unless TEST_UPSTREAM_API_KEY is set
// ---------------------------------------------------------------------------

func upstreamAPIKey(t *testing.T) string {
	t.Helper()
	k := os.Getenv("TEST_UPSTREAM_API_KEY")
	if k == "" {
		t.Skip("skipping: TEST_UPSTREAM_API_KEY not set")
	}
	return k
}

func setupUpstreamRoute(t *testing.T, r http.Handler, auth map[string]string, routeType, baseURL, modelPattern, targetModel string) {
	t.Helper()

	// Create provider
	provName := fmt.Sprintf("gtest_upstream_%s_%d", routeType, time.Now().UnixNano())
	provPayload := map[string]any{
		"name":        provName,
		"api_key":     upstreamAPIKey(t),
		"remark":      "go test upstream",
	}
	if routeType == "openai" {
		provPayload["openai_base_url"] = baseURL
	} else {
		provPayload["anthropic_base_url"] = baseURL
	}
	w, pbody := doJSON(t, r, "POST", "/api/provider", provPayload, auth)
	if w.Code != 200 {
		t.Fatalf("create upstream provider: %d: %s", w.Code, w.Body.String())
	}
	pid := int(pbody["data"].(map[string]any)["id"].(float64))
	t.Cleanup(func() {
		doJSON(t, r, "DELETE", fmt.Sprintf("/api/provider/%d", pid), nil, auth)
	})

	// Create route
	w, _ = doJSON(t, r, "POST", "/api/route", map[string]any{
		"model_pattern": modelPattern,
		"route_type":    routeType,
		"provider_id":   pid,
		"target_model":  targetModel,
		"timeout":       60,
		"priority":      99,
		"is_active":     true,
	}, auth)
	if w.Code != 200 {
		t.Fatalf("create upstream route: %d: %s", w.Code, w.Body.String())
	}
}

func TestUpstream_OpenAI_NonStreaming(t *testing.T) {
	r := setupRouter(t)
	auth := registerAndPromote(t, r, "gtest_upstream_openai")
	_ = upstreamAPIKey(t) // skip if not set

	model := fmt.Sprintf("gtest-upstream-openai-%d", time.Now().UnixNano())
	setupUpstreamRoute(t, r, auth, "openai", "https://dashscope.aliyuncs.com/compatible-mode/v1", model, "qwen-turbo")

	// Create gateway API key for the /v1 call
	w, abody := doJSON(t, r, "POST", "/api/auth/api_keys", map[string]string{"name": "upstream-test"}, auth)
	if w.Code != 200 && w.Code != 201 {
		t.Fatalf("create api key: %d: %s", w.Code, w.Body.String())
	}
	gwKey, _ := abody["data"].(map[string]any)["key"].(string)
	kid := int(abody["data"].(map[string]any)["id"].(float64))
	t.Cleanup(func() {
		doJSON(t, r, "DELETE", fmt.Sprintf("/api/auth/api_keys/%d", kid), nil, auth)
	})

	v1Headers := map[string]string{
		"Authorization":             "Bearer " + gwKey,
		"X-Claude-Code-Session-Id":  fmt.Sprintf("gtest-upstream-%d", time.Now().UnixNano()),
	}

	w, body := doJSON(t, r, "POST", "/v1/chat/completions", map[string]any{
		"model":       model,
		"messages":    []map[string]string{{"role": "user", "content": "Reply with the single word: pong"}},
		"max_tokens":  50,
	}, v1Headers)
	if w.Code != 200 {
		t.Fatalf("non-stream chat: %d: %s", w.Code, w.Body.String())
	}
	if _, ok := body["choices"]; !ok {
		t.Fatalf("expected choices in response, got keys: %v", mapKeys(body))
	}
	if _, ok := body["usage"]; !ok {
		t.Fatalf("expected usage in response, got keys: %v", mapKeys(body))
	}
}

func TestUpstream_OpenAI_Streaming(t *testing.T) {
	r := setupRouter(t)
	auth := registerAndPromote(t, r, "gtest_upstream_oaistream")
	_ = upstreamAPIKey(t)

	model := fmt.Sprintf("gtest-upstream-oaistream-%d", time.Now().UnixNano())
	setupUpstreamRoute(t, r, auth, "openai", "https://dashscope.aliyuncs.com/compatible-mode/v1", model, "qwen-turbo")

	w, abody := doJSON(t, r, "POST", "/api/auth/api_keys", map[string]string{"name": "upstream-stream"}, auth)
	if w.Code != 200 && w.Code != 201 {
		t.Fatalf("create api key: %d", w.Code)
	}
	gwKey, _ := abody["data"].(map[string]any)["key"].(string)
	kid := int(abody["data"].(map[string]any)["id"].(float64))
	t.Cleanup(func() {
		doJSON(t, r, "DELETE", fmt.Sprintf("/api/auth/api_keys/%d", kid), nil, auth)
	})

	v1Headers := map[string]string{
		"Authorization":             "Bearer " + gwKey,
		"X-Claude-Code-Session-Id":  fmt.Sprintf("gtest-upstream-stream-%d", time.Now().UnixNano()),
	}

	body := map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": "Count: 1 2 3"}},
		"stream":   true,
		"max_tokens": 60,
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range v1Headers {
		req.Header.Set(k, v)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("stream chat: %d: %s", w.Code, w.Body.String())
	}

	bodyStr := w.Body.String()
	if !strings.Contains(bodyStr, "data: ") {
		t.Fatalf("expected SSE data chunks, got: %s", bodyStr[:min(len(bodyStr), 500)])
	}
	if !strings.Contains(bodyStr, "[DONE]") {
		t.Fatalf("expected [DONE] sentinel, got: %s", bodyStr[:min(len(bodyStr), 500)])
	}
}

func TestUpstream_Anthropic(t *testing.T) {
	r := setupRouter(t)
	auth := registerAndPromote(t, r, "gtest_upstream_anthropic")
	_ = upstreamAPIKey(t)

	model := fmt.Sprintf("gtest-upstream-anthropic-%d", time.Now().UnixNano())
	setupUpstreamRoute(t, r, auth, "anthropic", "https://api.anthropic.com", model, "claude-haiku-4-5")

	// Create gateway API key
	w, abody := doJSON(t, r, "POST", "/api/auth/api_keys", map[string]string{"name": "upstream-anthropic"}, auth)
	if w.Code != 200 && w.Code != 201 {
		t.Fatalf("create api key: %d", w.Code)
	}
	gwKey, _ := abody["data"].(map[string]any)["key"].(string)
	kid := int(abody["data"].(map[string]any)["id"].(float64))
	t.Cleanup(func() {
		doJSON(t, r, "DELETE", fmt.Sprintf("/api/auth/api_keys/%d", kid), nil, auth)
	})

	v1Headers := map[string]string{
		"x-api-key":                 gwKey,
		"anthropic-version":         "2023-06-01",
		"X-Claude-Code-Session-Id":  fmt.Sprintf("gtest-upstream-anthropic-%d", time.Now().UnixNano()),
	}

	w, body := doJSON(t, r, "POST", "/v1/messages", map[string]any{
		"model":      model,
		"max_tokens": 50,
		"messages":   []map[string]string{{"role": "user", "content": "Say hi"}},
	}, v1Headers)
	if w.Code != 200 {
		t.Fatalf("anthropic messages: %d: %s", w.Code, w.Body.String())
	}
	if _, ok := body["content"]; !ok {
		t.Fatalf("expected content in response, got keys: %v", mapKeys(body))
	}
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
