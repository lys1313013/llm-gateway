package config

import (
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	HTTPPort   int
	DBHost     string
	DBPort     string
	DBName     string
	DBUser     string
	DBPassword string
	DBTimezone string
	JWTSecret  string
	// JWTExpirationH 是登录 token 的有效期（小时），通过环境变量
	JWTExpirationH int
	LogLevel       string
	// SessionIDHeaders 是网关按顺序扫描的 session id 请求头列表，取第一个
	// 非空值写入 api_logs.session_id。通过环境变量 SESSION_ID_HEADERS
	// （逗号分隔）配置；默认沿用原有的 X-Claude-Code-Session-Id，
	// 保持向后兼容。
	SessionIDHeaders []string
}

var cfg *Config

// Load reads .env (if present) and populates the singleton config.
func Load() *Config {
	// .env is best-effort — containers will use real env vars.
	_ = godotenv.Load()

	cfg = &Config{
		HTTPPort:         getEnvInt("PORT", 5002),
		DBHost:           getEnv("DB_HOST", "localhost"),
		DBPort:           getEnv("DB_PORT", "5432"),
		DBName:           getEnv("DB_NAME", "llm_gateway"),
		DBUser:           getEnv("DB_USER", "postgres"),
		DBPassword:       getEnv("DB_PASSWORD", "password"),
		DBTimezone:       getEnvTZ("DB_TIMEZONE", "Asia/Shanghai"),
		JWTSecret:        getEnv("JWT_SECRET_KEY", "dev-secret-key-change-in-production"),
		JWTExpirationH:   getEnvInt("JWT_EXPIRATION_HOURS", 168),
		LogLevel:         getEnv("LOG_LEVEL", "info"),
		SessionIDHeaders: getEnvCSV("SESSION_ID_HEADERS", []string{"X-Claude-Code-Session-Id"}),
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

// getEnvCSV 读取逗号分隔的环境变量，返回去除空白、忽略空项的字符串切片。
// 未设置或全部为空时回退到 def。
func getEnvCSV(key string, def []string) []string {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return def
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return def
	}
	return out
}
