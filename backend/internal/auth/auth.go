// Package auth provides password hashing, JWT, and API key helpers.
//
// Password format: Werkzeug-compatible pbkdf2:sha256:<iterations>$<salt>$<hash>
// so hashes produced here are verifiable by both the Go and Python backends.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/scrypt"

	"github.com/lys1313013/llm-gateway/backend/internal/config"
	"github.com/lys1313013/llm-gateway/backend/internal/models"
)

const (
	// PasswordHashMethod is the prefix Werkzeug emits and the prefix we
	// emit. Format: pbkdf2:sha256:<iter>$<saltB64>$<hashB64>
	PasswordHashMethod = "pbkdf2:sha256"
	PasswordHashIters  = 600_000
	SaltBytes          = 16
	HashBytes          = 32
)

// ---------------------------------------------------------------------------
// Password
// ---------------------------------------------------------------------------

// HashPassword returns a Werkzeug-compatible pbkdf2:sha256:<iter>$<salt>$<hash>
// string. The salt is 16 random alphanumeric characters and the hash is hex
// encoded — the same format Werkzeug 3.x's generate_password_hash produces,
// so hashes are interchangeable between the two backends.
func HashPassword(password string) (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	salt := make([]byte, SaltBytes)
	alpha := make([]byte, SaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	for i, x := range salt {
		alpha[i] = alphabet[int(x)%len(alphabet)]
	}
	hash := pbkdf2([]byte(password), alpha, PasswordHashIters, HashBytes, sha256.New)
	return fmt.Sprintf("%s:%d$%s$%s",
		PasswordHashMethod, PasswordHashIters,
		string(alpha),
		hex.EncodeToString(hash),
	), nil
}

// VerifyPassword accepts pbkdf2:sha256:<iter>$<salt>$<hash> and
// scrypt:<N>:<r>:<p>$<salt>$<hash>.
//
// Werkzeug has shipped two salt encodings over the years:
//   - old (and what we emit): standard base64
//   - newer (Werkzeug 3.x default): 16 raw alphanumeric characters
//
// We try base64 first and fall back to treating the salt as raw bytes.
func VerifyPassword(password, encoded string) bool {
	if encoded == "" {
		return false
	}
	parts := strings.SplitN(encoded, "$", 3)
	if len(parts) != 3 {
		return false
	}
	methodParts := strings.SplitN(parts[0], ":", -1)
	if len(methodParts) < 2 {
		return false
	}
	switch methodParts[0] {
	case "pbkdf2":
		return verifyPBKDF2(password, methodParts, parts[1], parts[2])
	case "scrypt":
		return verifyScrypt(password, methodParts, parts[1], parts[2])
	default:
		return false
	}
}

func verifyPBKDF2(password string, methodParts []string, saltStr, hashStr string) bool {
	// expected: pbkdf2:<algo>:<iter>
	if len(methodParts) != 3 || methodParts[1] != "sha256" {
		return false
	}
	var iters int
	if _, err := fmt.Sscanf(methodParts[2], "%d", &iters); err != nil || iters <= 0 {
		return false
	}
	salt, ok := decodeSalt(saltStr)
	if !ok {
		return false
	}
	want, err := decodeHash(hashStr)
	if err != nil {
		return false
	}
	got := pbkdf2([]byte(password), salt, iters, len(want), sha256.New)
	return subtle.ConstantTimeCompare(got, want) == 1
}

func verifyScrypt(password string, methodParts []string, saltStr, hashStr string) bool {
	// expected: scrypt:<N>:<r>:<p>
	if len(methodParts) != 4 {
		return false
	}
	var n, r, p int
	for i, dst := range []*int{&n, &r, &p} {
		if _, err := fmt.Sscanf(methodParts[i+1], "%d", dst); err != nil || *dst <= 0 {
			return false
		}
	}
	salt, ok := decodeSalt(saltStr)
	if !ok {
		return false
	}
	want, err := decodeHash(hashStr)
	if err != nil {
		return false
	}
	got, err := scrypt.Key([]byte(password), salt, n, r, p, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// decodeSalt returns the salt bytes for a Werkzeug-format hash.
//
// Werkzeug has shipped three different salt encodings over the years:
//   - old (and what we emit): standard base64
//   - new (Werkzeug 3.x): 16 raw alphanumeric characters from a 62-char
//     alphabet
//   - URL-safe base64 (rare)
//
// We probe in order: try the raw string (only if it's purely alphanumeric —
// this matches the Werkzeug 3.x generator) → standard base64 → URL-safe
// base64. If nothing decodes, return false.
func decodeSalt(s string) ([]byte, bool) {
	if isAlnum(s) {
		return []byte(s), true
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, true
	}
	if b, err := base64.URLEncoding.DecodeString(s); err == nil {
		return b, true
	}
	return nil, false
}

func isAlnum(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// decodeHash accepts hex or base64-encoded hash bytes.
func decodeHash(s string) ([]byte, error) {
	if b, err := hex.DecodeString(s); err == nil && len(b) > 0 {
		return b, nil
	}
	return base64.StdEncoding.DecodeString(s)
}

// pbkdf2 is the standard PBKDF2 key-derivation function (RFC 2898).
// We implement it inline so we can keep zero non-stdlib dependencies for
// the auth surface.
func pbkdf2(password, salt []byte, iter, keyLen int, h func() hash.Hash) []byte {
	prf := hmac.New(h, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen

	out := make([]byte, 0, numBlocks*hashLen)
	var counter [4]byte
	for i := 1; i <= numBlocks; i++ {
		binary.BigEndian.PutUint32(counter[:], uint32(i))

		prf.Reset()
		prf.Write(salt)
		prf.Write(counter[:])
		u := prf.Sum(nil)

		t := make([]byte, hashLen)
		copy(t, u)
		for j := 2; j <= iter; j++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(nil)
			for k := 0; k < hashLen; k++ {
				t[k] ^= u[k]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}

// ---------------------------------------------------------------------------
// JWT
// ---------------------------------------------------------------------------

type Claims struct {
	UserID      int    `json:"sub_int"`
	Username    string `json:"username"`
	Role        int    `json:"role"`
	TeamID      *int   `json:"team_id,omitempty"`
	AuthVersion string `json:"auth_version"`
	jwt.RegisteredClaims
}

// authenticationVersion binds a login to the current credential and permission
// state without storing a password hash in the token or adding schema. Existing
// account mutations update updated_at, so changing a role away and back does not
// revive a previous token. HMAC prevents disclosing a password-hash fingerprint.
func authenticationVersion(user *models.User, secret string) string {
	state := struct {
		ID           int
		Username     string
		PasswordHash string
		Role         int
		TeamID       *int
		IsActive     bool
		CreatedAt    string
		UpdatedAt    string
	}{user.ID, user.Username, user.PasswordHash, user.Role, user.TeamID,
		user.IsActive, user.CreatedAt.UTC().Format(time.RFC3339Nano), user.UpdatedAt.UTC().Format(time.RFC3339Nano)}
	payload, _ := json.Marshal(state) // only scalar fields; cannot fail
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("llm-gateway:auth-state:v1\x00"))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func GenerateJWT(user *models.User) (string, error) {
	c := config.Get()
	if err := c.ValidateJWT(); err != nil {
		return "", err
	}
	if user == nil || user.ID <= 0 || !user.IsActive || user.PasswordHash == "" || user.Role < 1 || user.Role > 3 {
		return "", errors.New("cannot issue a token for an invalid or inactive user")
	}
	now := time.Now()
	claims := Claims{
		UserID:      user.ID,
		Username:    user.Username,
		Role:        user.Role,
		TeamID:      user.TeamID,
		AuthVersion: authenticationVersion(user, c.JWTSecret),
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprintf("%d", user.ID),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(c.JWTExpirationH) * time.Hour)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString([]byte(c.JWTSecret))
}

func DecodeJWT(tokenStr string) (*Claims, error) {
	c := config.Get()
	if err := c.ValidateJWT(); err != nil {
		return nil, err
	}
	tok, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		return []byte(c.JWTSecret), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if err != nil {
		return nil, err
	}
	claims, ok := tok.Claims.(*Claims)
	if !ok || !tok.Valid || claims.UserID <= 0 || claims.Subject != fmt.Sprintf("%d", claims.UserID) || claims.IssuedAt == nil || claims.AuthVersion == "" {
		return nil, errors.New("invalid token claims; sign in again")
	}
	return claims, nil
}

// ValidateCurrentUser must run against a freshly loaded account on every JWT
// request. Deleted/disabled accounts and stale credentials or permissions fail
// closed. Legacy tokens without auth_version must sign in again after upgrade.
func ValidateCurrentUser(claims *Claims, user *models.User) error {
	if claims == nil || user == nil || !user.IsActive || user.ID <= 0 || user.ID != claims.UserID || user.PasswordHash == "" || user.Role < 1 || user.Role > 3 {
		return errors.New("account unavailable")
	}
	c := config.Get()
	if err := c.ValidateJWT(); err != nil {
		return err
	}
	if claims.Username != user.Username || claims.Role != user.Role || !sameTeam(claims.TeamID, user.TeamID) ||
		!hmac.Equal([]byte(claims.AuthVersion), []byte(authenticationVersion(user, c.JWTSecret))) {
		return errors.New("authentication state changed; sign in again")
	}
	return nil
}

func sameTeam(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// ---------------------------------------------------------------------------
// API key
// ---------------------------------------------------------------------------

// GenerateAPIKey returns (fullKey, keyHash, keyPrefix).
func GenerateAPIKey() (string, string, string) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		panic(err) // rand failure is unrecoverable
	}
	full := "sk-" + hex.EncodeToString(raw)
	hash := HashAPIKey(full)
	prefix := full[:10] + "..."
	return full, hash, prefix
}

func HashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}
