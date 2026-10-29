package auth

import (
	"github.com/golang-jwt/jwt/v5"
	"github.com/lys1313013/llm-gateway/backend/internal/config"
	"github.com/lys1313013/llm-gateway/backend/internal/models"
	"testing"
	"time"
)

func securityConfig(t *testing.T) *config.Config {
	t.Helper()
	t.Setenv("JWT_SECRET_KEY", "unit-tests-only-32-byte-secret-not-for-deployment")
	t.Setenv("JWT_EXPIRATION_HOURS", "24")
	return config.Load()
}

func securityUser() *models.User {
	teamID := 12
	return &models.User{ID: 7, Username: "synthetic-user", PasswordHash: "synthetic-password-hash", IsActive: true, Role: 2, TeamID: &teamID,
		CreatedAt: time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(200, 123000).UTC()}
}

func TestJWTRejectsRevokedAccountState(t *testing.T) {
	securityConfig(t)
	original := securityUser()
	token, err := GenerateJWT(original)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := DecodeJWT(token)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCurrentUser(claims, original); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(*models.User) *models.User
	}{
		{"deleted", func(u *models.User) *models.User { return nil }},
		{"disabled", func(u *models.User) *models.User { u.IsActive = false; return u }},
		{"password changed", func(u *models.User) *models.User { u.PasswordHash = "new-synthetic-password-hash"; return u }},
		{"demoted", func(u *models.User) *models.User { u.Role = 3; return u }},
		{"changed team", func(u *models.User) *models.User { team := 99; u.TeamID = &team; return u }},
		{"removed team", func(u *models.User) *models.User { u.TeamID = nil; return u }},
		{"restored role after mutation", func(u *models.User) *models.User { u.UpdatedAt = u.UpdatedAt.Add(time.Second); return u }},
		{"recreated account", func(u *models.User) *models.User { u.CreatedAt = u.CreatedAt.Add(time.Second); return u }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			u := *original
			current := tt.mutate(&u)
			if err := ValidateCurrentUser(claims, current); err == nil {
				t.Fatal("old token accepted after account state changed")
			}
			if current != nil && current.IsActive {
				fresh, err := GenerateJWT(current)
				if err != nil {
					t.Fatal(err)
				}
				freshClaims, err := DecodeJWT(fresh)
				if err != nil {
					t.Fatal(err)
				}
				if err := ValidateCurrentUser(freshClaims, current); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	same := *original
	same.UpdatedAt = same.UpdatedAt.In(time.FixedZone("other", 8*3600))
	if err := ValidateCurrentUser(claims, &same); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeJWTRejectsLegacyAndMalformedTokens(t *testing.T) {
	cfg := securityConfig(t)
	user := securityUser()
	cases := []struct {
		name   string
		modify func(*Claims)
		method jwt.SigningMethod
	}{
		{"missing version", func(c *Claims) { c.AuthVersion = "" }, jwt.SigningMethodHS256},
		{"missing expiry", func(c *Claims) { c.ExpiresAt = nil }, jwt.SigningMethodHS256},
		{"expired", func(c *Claims) { c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Hour)) }, jwt.SigningMethodHS256},
		{"missing issued at", func(c *Claims) { c.IssuedAt = nil }, jwt.SigningMethodHS256},
		{"subject mismatch", func(c *Claims) { c.Subject = "8" }, jwt.SigningMethodHS256},
		{"invalid user id", func(c *Claims) { c.UserID = -1 }, jwt.SigningMethodHS256},
		{"unexpected HMAC algorithm", func(c *Claims) {}, jwt.SigningMethodHS384},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			token, err := GenerateJWT(user)
			if err != nil {
				t.Fatal(err)
			}
			claims, err := DecodeJWT(token)
			if err != nil {
				t.Fatal(err)
			}
			tt.modify(claims)
			invalid, err := jwt.NewWithClaims(tt.method, claims).SignedString([]byte(cfg.JWTSecret))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeJWT(invalid); err == nil {
				t.Fatal("invalid token accepted")
			}
		})
	}
}

func TestJWTRejectsDefaultSecretAndRotationInvalidatesTokens(t *testing.T) {
	cfg := securityConfig(t)
	token, err := GenerateJWT(securityUser())
	if err != nil {
		t.Fatal(err)
	}
	cfg.JWTSecret = "second-unit-test-32-byte-signing-key-for-rotation"
	if _, err := DecodeJWT(token); err == nil {
		t.Fatal("old signing key accepted after rotation")
	}
	cfg.JWTSecret = "change-me-to-a-random-secret-string"
	if _, err := GenerateJWT(securityUser()); err == nil {
		t.Fatal("issued token using public default key")
	}
	if _, err := DecodeJWT(token); err == nil {
		t.Fatal("accepted token while default key configured")
	}
}
