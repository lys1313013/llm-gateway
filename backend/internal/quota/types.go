package quota

import "time"

// DisplayType identifies how the frontend should render a snapshot.
const (
	DisplayTypeModelRemains = "model_remains"
	DisplayTypeBalance      = "balance"
)

// Format identifiers (the value stored in provider.quota_format).
const (
	FormatMiniMax  = "minimax"
	FormatDeepSeek = "deepseek"
	FormatKimi     = "kimi"
)

// Snapshot is the unified envelope returned by the cache to handlers.
// Exactly one of Models / Balance is populated, chosen by DisplayType.
type Snapshot struct {
	DisplayType string           `json:"display_type"`
	Models      []ModelQuota     `json:"models,omitempty"`
	Balance     *BalanceSnapshot `json:"balance,omitempty"`
	LastError   string           `json:"last_error,omitempty"`
	FetchedAt   time.Time        `json:"fetched_at"`
}

// ModelQuota captures MiniMax-style per-model quota. Two parallel cycles
// (interval / weekly) are exposed separately because they have different
// reset cadences.
type ModelQuota struct {
	ModelName  string `json:"model_name"`
	Status     int    `json:"status"`
	StatusText string `json:"status_text"`

	IntervalUsageCount *int64     `json:"interval_usage_count,omitempty"`
	IntervalTotalCount *int64     `json:"interval_total_count,omitempty"`
	IntervalUsedPct    int        `json:"interval_used_percent"`
	IntervalRemainsMs  int64      `json:"interval_remains_ms,omitempty"`
	IntervalStartTime  *time.Time `json:"interval_start_time,omitempty"`
	IntervalEndTime    *time.Time `json:"interval_end_time,omitempty"`

	WeeklyUsageCount *int64     `json:"weekly_usage_count,omitempty"`
	WeeklyTotalCount *int64     `json:"weekly_total_count,omitempty"`
	WeeklyUsedPct    int        `json:"weekly_used_percent"`
	WeeklyRemainsMs  int64      `json:"weekly_remains_ms,omitempty"`
	WeeklyStartTime  *time.Time `json:"weekly_start_time,omitempty"`
	WeeklyEndTime    *time.Time `json:"weekly_end_time,omitempty"`
}

// BalanceSnapshot captures DeepSeek-style single-account balance.
type BalanceSnapshot struct {
	IsAvailable bool   `json:"is_available"`
	Currency    string `json:"currency"`
	Total       string `json:"total"`
	Granted     string `json:"granted"`
	ToppedUp    string `json:"topped_up"`
}
