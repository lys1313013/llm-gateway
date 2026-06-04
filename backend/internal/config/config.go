package config

import (
	"fmt"
	"log/slog"
	"os"
	"regexp"

	"github.com/joho/godotenv"
)

type Config struct {
	HTTPPort       int
	DBHost         string
	DBPort         string
	DBName         string
	DBUser         string
	DBPassword     string
	DBTimezone     string
	JWTSecret      string
	JWTExpirationH int
	LogLevel       string
}

var cfg *Config

// Load reads .env (if present) and populates the singleton config.
func Load() *Config {
	// .env is best-effort — containers will use real env vars.
	_ = godotenv.Load()

	cfg = &Config{
		HTTPPort:       getEnvInt("PORT", 5002),
		DBHost:         getEnv("DB_HOST", "localhost"),
		DBPort:         getEnv("DB_PORT", "5432"),
		DBName:         getEnv("DB_NAME", "llm_gateway"),
		DBUser:         getEnv("DB_USER", "postgres"),
		DBPassword:     getEnv("DB_PASSWORD", "password"),
		DBTimezone:     getEnvTZ("DB_TIMEZONE", "Asia/Shanghai"),
		JWTSecret:      getEnv("JWT_SECRET_KEY", "dev-secret-key-change-in-production"),
		JWTExpirationH: getEnvInt("JWT_EXPIRATION_HOURS", 24),
		LogLevel:       getEnv("LOG_LEVEL", "info"),
	}
	return cfg
}

// Get returns the loaded config (Load must have been called first).
func Get() *Config {
	if cfg == nil {
		return Load()
	}
	return cfg
}

// ConnInfo returns a libpq-style connection string.
func (c *Config) ConnInfo() string {
	return fmt.Sprintf("host=%s port=%s dbname=%s user=%s password=%s",
		c.DBHost, c.DBPort, c.DBName, c.DBUser, c.DBPassword)
}

func getEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
		slog.Warn("invalid int env var, using default", "key", key, "value", v, "default", def)
	}
	return def
}

var tzRe = regexp.MustCompile(`^[A-Za-z0-9/_+\-]+$`)

func getEnvTZ(key, def string) string {
	v := getEnv(key, def)
	if tzRe.MatchString(v) {
		return v
	}
	slog.Warn("invalid DB_TIMEZONE, falling back to UTC", "value", v)
	return "UTC"
}
