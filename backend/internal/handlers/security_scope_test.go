package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lys1313013/llm-gateway/backend/internal/config"
	"github.com/lys1313013/llm-gateway/backend/internal/db"
	"github.com/lys1313013/llm-gateway/backend/internal/middleware"
)

func securityContext(t *testing.T, userID, role, teamID int) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(middleware.CtxUserID, userID)
	c.Set(middleware.CtxUserRole, role)
	if teamID > 0 {
		c.Set(middleware.CtxTeamID, &teamID)
	}
	c.Request = httptest.NewRequest("GET", "/", nil)
	return c, w
}

func TestSecurityLogScopeFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name            string
		uid, role, team int
		want            db.LogScope
	}{
		{"root", 1, 1, 0, db.LogScope{All: true}},
		{"admin", 2, 2, 10, db.LogScope{TeamID: 10}},
		{"common", 3, 3, 10, db.LogScope{UserID: 3}},
		{"common without team", 3, 3, 0, db.LogScope{UserID: 3}},
		{"admin without team", 2, 2, 0, db.LogScope{}},
		{"missing identity", 0, 1, 10, db.LogScope{}},
		{"unknown role", 3, 99, 10, db.LogScope{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := securityContext(t, tc.uid, tc.role, tc.team)
			if got := logScope(c); got != tc.want {
				t.Fatalf("scope=%+v want %+v", got, tc.want)
			}
		})
	}
}

func TestSecurityTestRouteTeamBoundary(t *testing.T) {
	for _, tc := range []struct {
		name            string
		uid, role, team int
		request         any
		present         bool
		want            int
		allowed         bool
		status          int
	}{
		{"root foreign team", 1, 1, 10, float64(20), true, 20, true, 200},
		{"root without default team", 1, 1, 0, float64(20), true, 20, true, 200},
		{"admin own team", 2, 2, 10, float64(10), true, 10, true, 200},
		{"admin foreign team", 2, 2, 10, float64(20), true, 0, false, 403},
		{"common own team", 3, 3, 10, float64(10), true, 10, true, 200},
		{"common foreign team", 3, 3, 10, float64(20), true, 0, false, 403},
		{"common no team", 3, 3, 0, float64(20), true, 0, false, 403},
		{"missing team", 3, 3, 0, nil, false, 0, false, 403},
		{"fractional", 1, 1, 10, 10.5, true, 0, false, 400},
		{"string", 1, 1, 10, "20", true, 0, false, 400},
		{"zero", 1, 1, 10, float64(0), true, 0, false, 400},
		{"no identity", 0, 1, 10, float64(20), true, 0, false, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, w := securityContext(t, tc.uid, tc.role, tc.team)
			data := map[string]any{}
			if tc.present {
				data["team_id"] = tc.request
			}
			got, ok := testRouteTeamID(c, data)
			if got != tc.want || ok != tc.allowed || w.Code != tc.status {
				t.Fatalf("team=%d allowed=%v status=%d", got, ok, w.Code)
			}
		})
	}
}

// These integration tests require an explicit disposable database, never the
// development .env or TEST_DB_* connection used by the legacy test suite.
func securityDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SECURITY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SECURITY_TEST_DATABASE_URL is not set")
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") || strings.Trim(u.Path, "/") != "security_fix" {
		t.Fatal("security tests require a loopback disposable security_fix database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("scope_test_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	old := db.Pool
	db.Pool = pool
	t.Cleanup(func() {
		db.Pool = old
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	t.Setenv("DB_TIMEZONE", "UTC")
	config.Load()
	if err = db.Migrate(ctx, db.MigrateOptions{AppVersion: "security-test"}); err != nil {
		t.Fatal(err)
	}
	return pool
}

func securityRequest(t *testing.T, handler gin.HandlerFunc, uid, role, team int, method, path, param string, body any) (int, map[string]any) {
	t.Helper()
	c, w := securityContext(t, uid, role, team)
	raw, _ := json.Marshal(body)
	c.Request = httptest.NewRequest(method, path, bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	if param != "" {
		c.Params = gin.Params{{Key: "id", Value: param}}
	}
	handler(c)
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("response %d: %s", w.Code, w.Body.String())
	}
	return w.Code, out
}

func securityFixture(t *testing.T, pool *pgxpool.Pool) (team1, team2, owner, peer, foreign int) {
	t.Helper()
	ctx := context.Background()
	for i, p := range []*int{&team1, &team2} {
		if err := pool.QueryRow(ctx, `INSERT INTO team (name) VALUES ($1) RETURNING id`, fmt.Sprintf("scope-team-%d", i)).Scan(p); err != nil {
			t.Fatal(err)
		}
	}
	for i, p := range []*int{&owner, &peer, &foreign} {
		team := team1
		if i == 2 {
			team = team2
		}
		if err := pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,team_id) VALUES($1,'synthetic-not-a-login-hash',$2) RETURNING id`, fmt.Sprintf("scope-user-%d", i), team).Scan(p); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func TestSecuritySessionsAndLogsIsolation(t *testing.T) {
	pool := securityDatabase(t)
	ctx := context.Background()
	team1, _, owner, peer, foreign := securityFixture(t, pool)
	ids := make([]int, 4)
	for i, who := range []any{owner, peer, foreign, nil} {
		err := pool.QueryRow(ctx, `INSERT INTO api_logs(session_id,user_id,model,status_code,protocol,total_tokens,last_message_preview,request_data) VALUES('shared',$1,$2,$3,$4,10,$2,'{"test":true}') RETURNING id`, who, fmt.Sprintf("model%d", i), 200+i, fmt.Sprintf("protocol%d", i)).Scan(&ids[i])
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO api_logs(session_id,user_id,model,total_tokens,status_code) VALUES('foreign-only',$1,'foreign',50,500)`, foreign); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                  string
		uid, role, team, want int
	}{
		{"common", owner, 3, team1, 1}, {"team admin", owner, 2, team1, 2}, {"root", owner, 1, 0, 4}, {"teamless admin", owner, 2, 0, 0}, {"missing identity", 0, 1, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, out := securityRequest(t, ListSessions, tc.uid, tc.role, tc.team, "GET", "/api/sessions?q=shared", "", nil)
			if status != 200 {
				t.Fatalf("list: %v", out)
			}
			rows := out["data"].([]any)
			wantSessions := 0
			if tc.want > 0 {
				wantSessions = 1
			}
			if len(rows) != wantSessions || int(out["total"].(float64)) != wantSessions {
				t.Fatalf("session list/count: %v", out)
			}
			if tc.want > 0 {
				s := rows[0].(map[string]any)
				if int(s["request_count"].(float64)) != tc.want || len(s["models"].([]any)) != tc.want || len(s["status_summary"].(map[string]any)) != tc.want || len(s["protocol_summary"].(map[string]any)) != tc.want {
					t.Fatalf("summary leaked: %v", s)
				}
			}
			status, out = securityRequest(t, GetSession, tc.uid, tc.role, tc.team, "GET", "/api/sessions/shared?limit=1&offset=0", "shared", nil)
			if tc.want == 0 {
				if status != 404 {
					t.Fatalf("expected hidden session: %v", out)
				}
				return
			}
			if status != 200 || int(out["total"].(float64)) != tc.want || len(out["data"].([]any)) != 1 {
				t.Fatalf("session detail pagination/count: %v", out)
			}
			meta := out["meta"].(map[string]any)
			if int(meta["request_count"].(float64)) != tc.want || int(meta["total_tokens"].(float64)) != tc.want*10 {
				t.Fatalf("meta leaked: %v", meta)
			}
			status, out = securityRequest(t, ListLogs, tc.uid, tc.role, tc.team, "GET", "/api/logs?model=model&request_search=test&limit=1", "", nil)
			if status != 200 || int(out["total"].(float64)) != tc.want || len(out["data"].([]any)) != 1 {
				t.Fatalf("log list/count: %v", out)
			}
			for _, index := range []int{2, 3} {
				status, out = securityRequest(t, GetLogDetail, tc.uid, tc.role, tc.team, "GET", "/api/logs/x", fmt.Sprint(ids[index]), nil)
				expected := 404
				if tc.role == 1 {
					expected = 200
				}
				if status != expected {
					t.Fatalf("foreign/orphan log detail status=%d expected=%d %v", status, expected, out)
				}
			}
			status, out = securityRequest(t, GetSession, tc.uid, tc.role, tc.team, "GET", "/api/sessions/foreign-only", "foreign-only", nil)
			expected := 404
			if tc.role == 1 {
				expected = 200
			}
			if status != expected {
				t.Fatalf("foreign session status=%d", status)
			}
		})
	}
	// Every aggregate uses the same owner/team scope, not only the detail API.
	today := time.Now().UTC().Format("2006-01-02")
	for _, tc := range []struct {
		name                      string
		role, team, count, tokens int
	}{
		{"common", 3, team1, 1, 10}, {"team admin", 2, team1, 2, 20}, {"root", 1, 0, 5, 90}, {"teamless admin", 2, 0, 0, 0},
	} {
		t.Run("stats/"+tc.name, func(t *testing.T) {
			status, out := securityRequest(t, TodayStats, owner, tc.role, tc.team, "GET", "/api/stats", "", nil)
			if status != 200 {
				t.Fatalf("today stats failed: %v", out)
			}
			data := out["data"].(map[string]any)
			if int(data["total_requests"].(float64)) != tc.count || int(data["total_tokens"].(float64)) != tc.tokens {
				t.Fatalf("today stats leaked: %v", out)
			}
			status, out = securityRequest(t, ListStatusCodes, owner, tc.role, tc.team, "GET", "/api/logs/status_codes", "", nil)
			if status != 200 || len(out["data"].([]any)) != tc.count {
				t.Fatalf("status codes leaked: %v", out)
			}
			for _, end := range []string{today, time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")} {
				status, out = securityRequest(t, DailyTokenStats, owner, tc.role, tc.team, "GET", "/api/stats?start_date="+today+"&end_date="+end, "", nil)
				if status != 200 {
					t.Fatalf("token stats failed: %v", out)
				}
				data = out["data"].(map[string]any)
				field := "daily"
				if end == today {
					field = "hourly"
				}
				var count, tokens int
				if rows, ok := data[field].([]any); ok {
					for _, raw := range rows {
						row := raw.(map[string]any)
						count += int(row["request_count"].(float64))
						tokens += int(row["total_tokens"].(float64))
					}
				}
				if count != tc.count || tokens != tc.tokens {
					t.Fatalf("%s stats leaked: %v", field, out)
				}
				count = 0
				if rows, ok := data["models"].([]any); ok {
					for _, raw := range rows {
						count += int(raw.(map[string]any)["request_count"].(float64))
					}
				}
				if count != tc.count {
					t.Fatalf("model stats leaked: %v", out)
				}
			}
		})
	}
	status, _ := securityRequest(t, DeleteLog, owner, 2, team1, "DELETE", "/api/logs/x", fmt.Sprint(ids[2]), nil)
	if status != 404 {
		t.Fatalf("admin foreign delete status=%d", status)
	}
	status, out := securityRequest(t, DeleteSession, owner, 2, team1, "DELETE", "/api/sessions/shared", "shared", nil)
	if status != 200 || int(out["data"].(map[string]any)["deleted_logs"].(float64)) != 2 {
		t.Fatalf("scoped deletion: %v", out)
	}
	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM api_logs WHERE session_id='shared'`).Scan(&remaining); err != nil || remaining != 2 {
		t.Fatalf("foreign/orphan rows lost: %d %v", remaining, err)
	}
}

func TestSecurityModelTestsRespectTeamAndExposure(t *testing.T) {
	pool := securityDatabase(t)
	ctx := context.Background()
	team1, team2, owner, _, _ := securityFixture(t, pool)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var data map[string]any
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			t.Error(err)
		}
		if _, present := data["team_id"]; present {
			t.Error("gateway team selector forwarded upstream")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"local-test","object":"chat.completion","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer upstream.Close()
	var provider int
	if err := pool.QueryRow(ctx, `INSERT INTO provider(name,openai_base_url,anthropic_base_url) VALUES('local-mock',$1,$1) RETURNING id`, upstream.URL).Scan(&provider); err != nil {
		t.Fatal(err)
	}
	for _, team := range []int{team1, team2} {
		if _, err := pool.Exec(ctx, `INSERT INTO model_route(model_pattern,route_type,provider_id,team_id) VALUES('*','openai',$1,$2)`, provider, team); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO exposed_model(model_id,team_id,is_active) VALUES('allowed',$1,true),('disabled',$1,false)`, team1); err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []struct {
		name    string
		handler gin.HandlerFunc
	}{{"chat", TestChat}, {"messages", TestMessages}} {
		for _, tc := range []struct {
			name       string
			role, team int
			model      string
			want       int
		}{
			{"common foreign", 3, team2, "allowed", 403},
			{"admin foreign", 2, team2, "allowed", 403},
			{"common hidden model", 3, team1, "hidden", 403},
			{"common disabled model", 3, team1, "disabled", 403},
			{"common allowed", 3, team1, "allowed", 200},
			{"admin own unexposed model", 2, team1, "hidden", 200},
			{"root foreign unexposed model", 1, team2, "hidden", 200},
		} {
			t.Run(protocol.name+"/"+tc.name, func(t *testing.T) {
				before := calls.Load()
				status, out := securityRequest(t, protocol.handler, owner, tc.role, team1, "POST", "/api/test/"+protocol.name, "", map[string]any{"team_id": tc.team, "model": tc.model, "messages": []any{map[string]any{"role": "user", "content": "local test"}}, "max_tokens": 1})
				if status != tc.want {
					t.Fatalf("status=%d want=%d response=%v", status, tc.want, out)
				}
				expected := int32(0)
				if tc.want == 200 {
					expected = 1
				}
				if got := calls.Load() - before; got != expected {
					t.Fatalf("upstream calls=%d expected=%d", got, expected)
				}
			})
		}
	}
}
