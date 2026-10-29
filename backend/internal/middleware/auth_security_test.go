package middleware

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/lys1313013/llm-gateway/backend/internal/auth"
	"github.com/lys1313013/llm-gateway/backend/internal/config"
	"github.com/lys1313013/llm-gateway/backend/internal/models"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestJWTGuardChecksCurrentAccountOnEveryRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET_KEY", "middleware-unit-tests-32-byte-secret-not-for-deployment")
	t.Setenv("JWT_EXPIRATION_HOURS", "24")
	config.Load()
	team := 7
	original := models.User{ID: 42, Username: "synthetic-admin", Role: 2, TeamID: &team, IsActive: true, PasswordHash: "synthetic-hash", UpdatedAt: time.Unix(100, 0)}
	token, err := auth.GenerateJWT(&original)
	if err != nil {
		t.Fatal(err)
	}
	current := &original
	var lookupErr error
	lookups := 0
	called := 0
	r := gin.New()
	r.Use(requireAuth(func(ctx context.Context, id int) (*models.User, error) {
		lookups++
		if id != original.ID {
			t.Errorf("unexpected user lookup")
		}
		return current, lookupErr
	}))
	r.GET("/api/protected", RequireAdmin, func(c *gin.Context) {
		called++
		if c.GetInt(CtxUserID) != original.ID || c.GetString(CtxUsername) != original.Username || GetUserRole(c) != 2 || *GetTeamID(c) != team {
			t.Error("context does not match current authenticated account")
		}
		c.Status(http.StatusNoContent)
	})
	request := func(want int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/protected", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("got %d,want %d", w.Code, want)
		}
	}
	request(http.StatusNoContent)
	cases := []struct {
		name   string
		change func(*models.User)
	}{
		{"password", func(u *models.User) { u.PasswordHash = "changed-hash" }},
		{"role", func(u *models.User) { u.Role = 3 }},
		{"team", func(u *models.User) { u.TeamID = nil }},
		{"disabled", func(u *models.User) { u.IsActive = false }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) { u := original; current = &u; tt.change(current); request(http.StatusUnauthorized) })
	}
	current = nil
	request(http.StatusUnauthorized)
	lookupErr = errors.New("synthetic database failure")
	request(http.StatusInternalServerError)
	if lookups != 7 || called != 1 {
		t.Fatalf("lookups=%d protected handler calls=%d", lookups, called)
	}
}
