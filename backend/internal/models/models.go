package models

import (
	"encoding/json"
	"time"
)

// ---------------------------------------------------------------------------
// Domain models — mirror the SQL tables in db.py exactly
// ---------------------------------------------------------------------------

type Provider struct {
	ID               int       `json:"id"`
	Name             string    `json:"name"`
	OpenAIBaseURL    *string   `json:"openai_base_url,omitempty"`
	AnthropicBaseURL *string   `json:"anthropic_base_url,omitempty"`
	APIKey           *string   `json:"api_key,omitempty"`
	Remark           *string   `json:"remark,omitempty"`
	QuotaURL         *string   `json:"quota_url,omitempty"`
	QuotaFormat      *string   `json:"quota_format,omitempty"`
	CreateTime       time.Time `json:"create_time"`
	UpdateTime       time.Time `json:"update_time"`
}

type ModelRoute struct {
	ID            int       `json:"id"`
	ModelPattern  string    `json:"model_pattern"`
	RouteType     string    `json:"route_type"`
	ProviderID    *int      `json:"provider_id,omitempty"`
	TargetModel   *string   `json:"target_model,omitempty"`
	Timeout       int       `json:"timeout"`
	LogRequests   bool      `json:"log_requests"`
	LogResponses  bool      `json:"log_responses"`
	Priority      int       `json:"priority"`
	IsActive      bool      `json:"is_active"`
	CreateTime    time.Time `json:"create_time"`
	UpdateTime    time.Time `json:"update_time"`

	// Joined fields from provider
	OpenAIBaseURL    *string `json:"openai_base_url,omitempty"`
	AnthropicBaseURL *string `json:"anthropic_base_url,omitempty"`
	APIKey           *string `json:"api_key,omitempty"`
	ProviderName     *string `json:"provider_name,omitempty"`
}

type ExposedModel struct {
	ID                   int        `json:"id"`
	ModelID              string     `json:"model_id"`
	OwnedBy              string     `json:"owned_by"`
	IsActive             bool       `json:"is_active"`
	LastOpenAITestTime   *time.Time `json:"last_openai_test_time,omitempty"`
	LastAnthropicTestTime *time.Time `json:"last_anthropic_test_time,omitempty"`
	CreateTime           time.Time  `json:"create_time"`
	UpdateTime           time.Time  `json:"update_time"`
}

type User struct {
	ID           int       `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	IsActive     bool      `json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type APIKey struct {
	ID         int        `json:"id"`
	UserID     int        `json:"user_id"`
	KeyHash    string     `json:"-"`
	KeyPrefix  string     `json:"key_prefix"`
	KeyValue   *string    `json:"key_value,omitempty"`
	Name       string     `json:"name"`
	IsActive   bool       `json:"is_active"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	Username   string     `json:"username,omitempty"` // joined
}

type APILog struct {
	ID                     int             `json:"id"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
	Model                  *string         `json:"model,omitempty"`
	ProviderID             *int            `json:"provider_id,omitempty"`
	ProviderName           *string         `json:"provider_name,omitempty"`
	IsStream               bool            `json:"is_stream"`
	StatusCode             *int            `json:"status_code,omitempty"`
	ProcessingTimeMs       *int            `json:"processing_time_ms,omitempty"`
	PromptTokens           *int            `json:"prompt_tokens,omitempty"`
	CompletionTokens       *int            `json:"completion_tokens,omitempty"`
	TotalTokens            *int            `json:"total_tokens,omitempty"`
	TargetURL              *string         `json:"target_url,omitempty"`
	RequestData            json.RawMessage `json:"request_data,omitempty"`
	ResponseData           json.RawMessage `json:"response_data,omitempty"`
	RequestHeaders         json.RawMessage `json:"request_headers,omitempty"`
	ResponseHeaders        json.RawMessage `json:"response_headers,omitempty"`
	ErrorMessage           *string         `json:"error_message,omitempty"`
	Protocol               *string         `json:"protocol,omitempty"`
	UsageData              json.RawMessage `json:"usage_data,omitempty"`
	CacheCreationInputTokens *int          `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens   *int            `json:"cache_read_input_tokens,omitempty"`
	SessionID              *string         `json:"session_id,omitempty"`
}

// ---------------------------------------------------------------------------
// Proxy config — passed from the chat/anthropic handlers to the proxy pkg
// ---------------------------------------------------------------------------

type ProxyConfig struct {
	TargetURL        string
	APIKey           string
	Timeout          int // seconds; -1 = no timeout
	LogRequests      bool
	LogResponses     bool
	Model            string
	Protocol         string // "openai" | "anthropic"
	AnthropicVersion string
	ProviderID       *int   // snapshot of model_route.provider_id at match time
	ProviderName     string // snapshot of provider.name at match time
	// RequestHeaders is the sanitized incoming request header map (auth
	// headers removed). Stored in api_logs.request_headers.
	RequestHeaders map[string]string
	// SessionID 是从配置的 session id 请求头（见 config.SessionIDHeaders）
	// 中解析出的值；客户端未传时为空。写入 api_logs.session_id，用于按
	// 会话聚合请求。
	SessionID string
	// IfEmpty is used by some callers as a fallback for AnthropicVersion.
	// Kept for parity with the Python ProxyConfig; the handler fills it in.
	IfEmpty string `json:"-"`
}

// NormalizedUsage is the canonical token-usage shape written to api_logs.
type NormalizedUsage struct {
	PromptTokens             int
	CompletionTokens         int
	TotalTokens              int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
	Raw                      []byte // original usage JSON, written to usage_data
}
