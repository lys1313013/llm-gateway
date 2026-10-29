package config

import (
	"strings"
	"testing"
)

func TestValidateJWTRejectsUnsafeSigningConfiguration(t *testing.T) {
	for _, secret := range []string{"", "short", "dev-secret-key-change-in-production", "change-me-to-a-random-secret-string", "your-jwt-secret-key-change-in-production", " " + strings.Repeat("a", 64)} {
		c := &Config{JWTSecret: secret, JWTExpirationH: 24}
		if err := c.ValidateJWT(); err == nil {
			t.Fatal("unsafe JWT configuration accepted")
		} else if secret != "" && strings.Contains(err.Error(), secret) {
			t.Fatal("validation error discloses supplied secret")
		}
	}
}

func TestValidateJWTRequiresBoundedPositiveLifetime(t *testing.T) {
	c := &Config{JWTSecret: "unit-tests-only-32-byte-secret-not-for-deployment", JWTExpirationH: 720}
	if err := c.ValidateJWT(); err != nil {
		t.Fatal(err)
	}
	for _, hours := range []int{-1, 0, 24*365 + 1} {
		c.JWTExpirationH = hours
		if err := c.ValidateJWT(); err == nil {
			t.Fatalf("accepted lifetime %d", hours)
		}
	}
}

func TestLoadDoesNotSupplyDefaultSigningSecret(t *testing.T) {
	t.Setenv("JWT_SECRET_KEY", "")
	previous := cfg
	t.Cleanup(func() { cfg = previous })
	if c := Load(); c.JWTSecret != "" || c.ValidateJWT() == nil {
		t.Fatal("missing secret must not silently fall back")
	}
}
