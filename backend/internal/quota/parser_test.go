package quota

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestDeepSeekParser_SingleCurrency(t *testing.T) {
	body := []byte(`{
		"is_available": true,
		"balance_infos": [
			{"currency":"CNY","total_balance":"100.00","granted_balance":"0.00","topped_up_balance":"100.00"}
		]
	}`)
	snap, err := Lookup(FormatDeepSeek).Parse(body)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if snap.DisplayType != DisplayTypeBalance {
		t.Errorf("display_type=%q want %q", snap.DisplayType, DisplayTypeBalance)
	}
	if snap.Balance == nil {
		t.Fatal("balance is nil")
	}
	if snap.Balance.Currency != "CNY" || snap.Balance.Total != "100.00" || snap.Balance.Granted != "0.00" || snap.Balance.ToppedUp != "100.00" {
		t.Errorf("balance mismatch: %+v", snap.Balance)
	}
	if !snap.Balance.IsAvailable {
		t.Error("IsAvailable should be true")
	}
	if snap.FetchedAt.IsZero() {
		t.Error("FetchedAt should be set")
	}
}

func TestDeepSeekParser_Unavailable(t *testing.T) {
	body := []byte(`{"is_available":false,"balance_infos":[]}`)
	_, err := Lookup(FormatDeepSeek).Parse(body)
	if err == nil {
		t.Fatal("expected error for unavailable account")
	}
	if !strings.Contains(err.Error(), "unavailable") {
		t.Errorf("err message should mention unavailable, got: %v", err)
	}
}

func TestDeepSeekParser_InvalidJSON(t *testing.T) {
	_, err := Lookup(FormatDeepSeek).Parse([]byte("not json"))
	if err == nil {
		t.Fatal("expected error for invalid json")
	}
}

func TestMiniMaxParser_Success(t *testing.T) {
	body := []byte(`{
		"base_resp": {"status_code": 0, "status_msg": "ok"},
		"model_remains": [
			{
				"model_name": "M1",
				"current_interval_status": 1,
				"current_interval_usage_count": 30,
				"current_interval_total_count": 100,
				"current_interval_remaining_percent": 70,
				"remains_time": 3600000,
				"start_time": 1718160000000,
				"end_time": 1718246400000,
				"current_weekly_status": 1,
				"current_weekly_usage_count": 300,
				"current_weekly_total_count": 1000,
				"current_weekly_remaining_percent": 70,
				"weekly_remains_time": 86400000,
				"weekly_start_time": 1718064000000,
				"weekly_end_time": 1718668800000
			}
		]
	}`)
	snap, err := Lookup(FormatMiniMax).Parse(body)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if snap.DisplayType != DisplayTypeModelRemains {
		t.Errorf("display_type=%q want %q", snap.DisplayType, DisplayTypeModelRemains)
	}
	if len(snap.Models) != 1 {
		t.Fatalf("models len=%d want 1", len(snap.Models))
	}
	m := snap.Models[0]
	if m.ModelName != "M1" {
		t.Errorf("model_name=%q", m.ModelName)
	}
	if m.IntervalUsedPct != 30 {
		t.Errorf("interval_used_percent=%d want 30 (100-70)", m.IntervalUsedPct)
	}
	if m.WeeklyUsedPct != 30 {
		t.Errorf("weekly_used_percent=%d want 30", m.WeeklyUsedPct)
	}
	if m.IntervalRemainsMs != 3600000 {
		t.Errorf("interval_remains_ms=%d", m.IntervalRemainsMs)
	}
	if m.Status != 1 || m.StatusText != "使用中" {
		t.Errorf("status=%d text=%q", m.Status, m.StatusText)
	}
}

func TestMiniMaxParser_StatusError(t *testing.T) {
	body := []byte(`{"base_resp":{"status_code":401,"status_msg":"unauthorized"}}`)
	_, err := Lookup(FormatMiniMax).Parse(body)
	if err == nil {
		t.Fatal("expected error for non-zero base_resp status")
	}
}

func TestKimiParser_Success(t *testing.T) {
	body := []byte(`{
		"user": {"userId":"u1","membership":{"level":"LEVEL_BASIC"}},
		"usage": {"limit":"100","used":"34","remaining":"66","resetTime":"2099-07-27T05:51:55.914424Z"},
		"limits": [{"window":{"duration":300,"timeUnit":"TIME_UNIT_MINUTE"},
			"detail":{"limit":"100","used":"2","remaining":"98","resetTime":"2099-07-22T17:51:55.914424Z"}}]
	}`)
	snap, err := Lookup(FormatKimi).Parse(body)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if snap.DisplayType != DisplayTypeModelRemains {
		t.Errorf("display_type=%q want %q", snap.DisplayType, DisplayTypeModelRemains)
	}
	if len(snap.Models) != 1 {
		t.Fatalf("models len=%d want 1", len(snap.Models))
	}
	m := snap.Models[0]
	if m.WeeklyUsedPct != 34 {
		t.Errorf("weekly_used_percent=%d want 34", m.WeeklyUsedPct)
	}
	if m.IntervalUsedPct != 2 {
		t.Errorf("interval_used_percent=%d want 2", m.IntervalUsedPct)
	}
	if m.WeeklyTotalCount == nil || *m.WeeklyTotalCount != 100 {
		t.Errorf("weekly_total_count=%v want 100", m.WeeklyTotalCount)
	}
	if m.IntervalTotalCount == nil || *m.IntervalTotalCount != 100 {
		t.Errorf("interval_total_count=%v want 100", m.IntervalTotalCount)
	}
	if m.WeeklyEndTime == nil || m.IntervalEndTime == nil {
		t.Error("reset times should be parsed")
	}
	if m.WeeklyRemainsMs <= 0 || m.IntervalRemainsMs <= 0 {
		t.Error("remains_ms should be positive for future reset times")
	}
}

// Regression: Kimi rounds "used" up to equal the limit near full usage; the
// percent must come from remaining, not a rounded-up 100.
func TestKimiParser_UsedRoundedUp_StillBelowLimit(t *testing.T) {
	body := []byte(`{
		"usage": {"limit":"100","used":"100","remaining":"1","resetTime":"2099-07-27T05:51:55.914424Z"},
		"limits": []
	}`)
	snap, err := Lookup(FormatKimi).Parse(body)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	m := snap.Models[0]
	if m.WeeklyUsedPct != 99 {
		t.Errorf("weekly_used_percent=%d want 99 (derived from remaining=1)", m.WeeklyUsedPct)
	}
	if m.WeeklyUsageCount == nil || *m.WeeklyUsageCount != 99 {
		t.Errorf("weekly_usage_count=%v want 99", m.WeeklyUsageCount)
	}
	if m.WeeklyTotalCount == nil || *m.WeeklyTotalCount != 100 {
		t.Errorf("weekly_total_count=%v want 100", m.WeeklyTotalCount)
	}
}

// Fractional counters: used=99.63 of 100 is 99%, never 100.
func TestKimiParser_FractionalRemaining(t *testing.T) {
	body := []byte(`{
		"usage": {"limit":"100","used":"100","remaining":"0.37","resetTime":"2099-07-27T05:51:55.914424Z"},
		"limits": []
	}`)
	snap, err := Lookup(FormatKimi).Parse(body)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	m := snap.Models[0]
	if m.WeeklyUsedPct >= 100 {
		t.Errorf("weekly_used_percent=%d want <100 for remaining=0.37", m.WeeklyUsedPct)
	}
}

// Real Kimi response: usage has integer used==limit and no remaining field.
// Without an authoritative remaining, used==limit means genuinely exhausted —
// must report 100%, not be masked to 99%.
func TestKimiParser_IntegerUsedEqualsLimit_IsExhausted(t *testing.T) {
	body := []byte(`{
		"usage": {"limit":"100","used":"100","resetTime":"2099-07-27T05:51:55.914424Z"},
		"limits": []
	}`)
	snap, err := Lookup(FormatKimi).Parse(body)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	m := snap.Models[0]
	if m.WeeklyUsedPct != 100 {
		t.Errorf("weekly_used_percent=%d want 100 (used==limit, no remaining)", m.WeeklyUsedPct)
	}
	if m.WeeklyUsageCount == nil || *m.WeeklyUsageCount != 100 {
		t.Errorf("weekly_usage_count=%v want 100", m.WeeklyUsageCount)
	}
	if m.WeeklyRemainsMs <= 0 {
		t.Error("weekly_remains_ms should be positive for a future reset time")
	}
}

// Full real-world response: usage exhausted (used==limit, no remaining) plus
// a short sliding window with remaining>0. Weekly must be 100%, interval
// derived from remaining.
func TestKimiParser_FullResponse_UsageExhausted(t *testing.T) {
	body := []byte(`{
		"user": {"userId":"u1","membership":{"level":"LEVEL_INTERMEDIATE"}},
		"usage": {"limit":"100","used":"100","resetTime":"2099-08-25T08:02:21.039284Z"},
		"limits": [{"window":{"duration":300,"timeUnit":"TIME_UNIT_MINUTE"},
			"detail":{"limit":"100","remaining":"100","resetTime":"2099-08-25T06:02:21.039284Z"}}],
		"parallel": {"limit":"20"}
	}`)
	snap, err := Lookup(FormatKimi).Parse(body)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	m := snap.Models[0]
	if m.WeeklyUsedPct != 100 {
		t.Errorf("weekly_used_percent=%d want 100", m.WeeklyUsedPct)
	}
	if m.WeeklyUsageCount == nil || *m.WeeklyUsageCount != 100 {
		t.Errorf("weekly_usage_count=%v want 100", m.WeeklyUsageCount)
	}
	// interval: remaining==limit → used=0
	if m.IntervalUsedPct != 0 {
		t.Errorf("interval_used_percent=%d want 0", m.IntervalUsedPct)
	}
	if m.IntervalUsageCount == nil || *m.IntervalUsageCount != 0 {
		t.Errorf("interval_usage_count=%v want 0", m.IntervalUsageCount)
	}
}

// used absent: must fall back to limit - remaining instead of 0%.
func TestKimiParser_MissingUsed(t *testing.T) {
	body := []byte(`{
		"usage": {"limit":"100","remaining":"26","resetTime":"2099-07-27T05:51:55.914424Z"},
		"limits": []
	}`)
	snap, err := Lookup(FormatKimi).Parse(body)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	m := snap.Models[0]
	if m.WeeklyUsedPct != 74 {
		t.Errorf("weekly_used_percent=%d want 74 (100-remaining)", m.WeeklyUsedPct)
	}
	if m.WeeklyUsageCount == nil || *m.WeeklyUsageCount != 74 {
		t.Errorf("weekly_usage_count=%v want 74", m.WeeklyUsageCount)
	}
}

func TestKimiParser_EmptyPayload(t *testing.T) {
	_, err := Lookup(FormatKimi).Parse([]byte(`{}`))
	if err == nil {
		t.Fatal("expected error for empty usage payload")
	}
}

func TestKimiParser_InvalidJSON(t *testing.T) {
	_, err := Lookup(FormatKimi).Parse([]byte("not json"))
	if err == nil {
		t.Fatal("expected error for invalid json")
	}
}

func TestOpenCodeGoParser_Success(t *testing.T) {
	body := []byte(`{
		"usage": {
			"rolling": {"status":"ok","percent":9,"resetsAt":"2099-08-31T10:00:00Z"},
			"weekly":  {"status":"ok","percent":12,"resetsAt":"2099-09-06T00:00:00Z"},
			"monthly": {"status":"ok","percent":6,"resetsAt":"2099-09-30T00:00:00Z"}
		}
	}`)
	snap, err := Lookup(FormatOpenCodeGo).Parse(body)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if snap.DisplayType != DisplayTypeModelRemains {
		t.Errorf("display_type=%q want %q", snap.DisplayType, DisplayTypeModelRemains)
	}
	if len(snap.Models) != 1 {
		t.Fatalf("models len=%d want 1", len(snap.Models))
	}
	m := snap.Models[0]
	if m.IntervalUsedPct != 9 {
		t.Errorf("interval_used_percent=%d want 9", m.IntervalUsedPct)
	}
	if m.WeeklyUsedPct != 12 {
		t.Errorf("weekly_used_percent=%d want 12", m.WeeklyUsedPct)
	}
	if m.MonthlyUsedPct == nil || *m.MonthlyUsedPct != 6 {
		t.Errorf("monthly_used_percent=%v want 6", m.MonthlyUsedPct)
	}
	if m.IntervalRemainsMs <= 0 || m.WeeklyRemainsMs <= 0 || m.MonthlyRemainsMs <= 0 {
		t.Error("remains_ms should be positive for future reset times")
	}
}

// percent==0: upstream resetsAt is a now+window placeholder and must be
// dropped, not shown as a countdown.
func TestOpenCodeGoParser_ZeroPercentDropsReset(t *testing.T) {
	body := []byte(`{
		"usage": {
			"rolling": {"status":"ok","percent":0,"resetsAt":"2099-08-31T10:00:00Z"},
			"weekly":  {"status":"rate-limited","percent":100,"resetsAt":"2099-09-06T00:00:00Z"}
		}
	}`)
	snap, err := Lookup(FormatOpenCodeGo).Parse(body)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	m := snap.Models[0]
	if m.IntervalUsedPct != 0 {
		t.Errorf("interval_used_percent=%d want 0", m.IntervalUsedPct)
	}
	if m.IntervalEndTime != nil || m.IntervalRemainsMs != 0 {
		t.Errorf("zero-percent window must drop resetsAt, got end=%v remains=%d", m.IntervalEndTime, m.IntervalRemainsMs)
	}
	// rate-limited arrives as percent=100 verbatim — no special-casing.
	if m.WeeklyUsedPct != 100 {
		t.Errorf("weekly_used_percent=%d want 100", m.WeeklyUsedPct)
	}
	// monthly window absent → pointer stays nil
	if m.MonthlyUsedPct != nil {
		t.Errorf("monthly_used_percent=%v want nil for absent window", *m.MonthlyUsedPct)
	}
}

// Old flat shape (rollingUsage/usagePercent/resetInSec) is long obsolete;
// an unrecognized payload with no known windows must fail loudly.
func TestOpenCodeGoParser_UnexpectedShape(t *testing.T) {
	for _, body := range []string{`{}`, `{"usage":{}}`, `{"rollingUsage":1,"usagePercent":2}`} {
		if _, err := Lookup(FormatOpenCodeGo).Parse([]byte(body)); err == nil {
			t.Errorf("expected error for shape %s", body)
		}
	}
}

func TestOpenCodeGoParser_InvalidJSON(t *testing.T) {
	_, err := Lookup(FormatOpenCodeGo).Parse([]byte("not json"))
	if err == nil {
		t.Fatal("expected error for invalid json")
	}
}

func TestChatGPTParser_Success(t *testing.T) {
	body := []byte(`{
		"plan_type": "plus",
		"rate_limit": {
			"allowed": true,
			"limit_reached": false,
			"primary_window": {"used_percent": 45, "limit_window_seconds": 18000, "reset_after_seconds": 1500, "reset_at": 4102444800},
			"secondary_window": {"used_percent": 56, "limit_window_seconds": 604800, "reset_after_seconds": 300000, "reset_at": 4102444800}
		}
	}`)
	snap, err := Lookup(FormatChatGPT).Parse(body)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if snap.DisplayType != DisplayTypeModelRemains {
		t.Errorf("display_type=%q want %q", snap.DisplayType, DisplayTypeModelRemains)
	}
	if len(snap.Models) != 1 {
		t.Fatalf("models len=%d want 1", len(snap.Models))
	}
	m := snap.Models[0]
	if m.ModelName != "ChatGPT plus" {
		t.Errorf("model_name=%q", m.ModelName)
	}
	if m.IntervalUsedPct != 45 {
		t.Errorf("interval_used_percent=%d want 45", m.IntervalUsedPct)
	}
	if m.WeeklyUsedPct != 56 {
		t.Errorf("weekly_used_percent=%d want 56", m.WeeklyUsedPct)
	}
	if m.IntervalRemainsMs <= 0 || m.WeeklyRemainsMs <= 0 {
		t.Error("remains_ms should be positive for future reset_at")
	}
	if m.IntervalEndTime == nil || m.WeeklyEndTime == nil {
		t.Error("end times should be parsed from reset_at")
	}
	if m.Status != 1 || m.StatusText != "使用中" {
		t.Errorf("status=%d text=%q", m.Status, m.StatusText)
	}
	// window length must pass through so the UI can label non-5h plans (Pro).
	if m.IntervalWindowSeconds == nil || *m.IntervalWindowSeconds != 18000 {
		t.Errorf("interval_window_seconds=%v want 18000", m.IntervalWindowSeconds)
	}
	if m.WeeklyWindowSeconds == nil || *m.WeeklyWindowSeconds != 604800 {
		t.Errorf("weekly_window_seconds=%v want 604800", m.WeeklyWindowSeconds)
	}
	if m.WeeklyPresent == nil || !*m.WeeklyPresent {
		t.Errorf("weekly_present=%v want true", m.WeeklyPresent)
	}
}

// secondary_window is null on some plans — weekly fields must stay zero.
func TestChatGPTParser_NoSecondaryWindow(t *testing.T) {
	body := []byte(`{
		"plan_type": "pro",
		"rate_limit": {
			"allowed": true,
			"limit_reached": false,
			"primary_window": {"used_percent": 10, "limit_window_seconds": 18000, "reset_after_seconds": 100, "reset_at": 4102444800},
			"secondary_window": null
		}
	}`)
	snap, err := Lookup(FormatChatGPT).Parse(body)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	m := snap.Models[0]
	if m.IntervalUsedPct != 10 {
		t.Errorf("interval_used_percent=%d want 10", m.IntervalUsedPct)
	}
	if m.WeeklyUsedPct != 0 || m.WeeklyEndTime != nil {
		t.Errorf("weekly fields should stay zero, got pct=%d end=%v", m.WeeklyUsedPct, m.WeeklyEndTime)
	}
	if m.WeeklyPresent == nil || *m.WeeklyPresent {
		t.Errorf("weekly_present=%v want explicit false for null secondary_window", m.WeeklyPresent)
	}
}

func TestChatGPTParser_LimitReached(t *testing.T) {
	body := []byte(`{
		"plan_type": "plus",
		"rate_limit": {
			"allowed": false,
			"limit_reached": true,
			"primary_window": {"used_percent": 100, "limit_window_seconds": 18000, "reset_after_seconds": 900, "reset_at": 4102444800},
			"secondary_window": {"used_percent": 80, "limit_window_seconds": 604800, "reset_after_seconds": 90000, "reset_at": 4102444800}
		}
	}`)
	snap, err := Lookup(FormatChatGPT).Parse(body)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	m := snap.Models[0]
	if m.Status != 2 || m.StatusText != "已限流" {
		t.Errorf("status=%d text=%q, want 2/已限流", m.Status, m.StatusText)
	}
	if m.IntervalUsedPct != 100 {
		t.Errorf("interval_used_percent=%d want 100", m.IntervalUsedPct)
	}
}

// used_percent=0: the window is idle and reset_at is a placeholder — drop it
// instead of showing a countdown (same rule as opencode_go).
func TestChatGPTParser_ZeroPercentDropsReset(t *testing.T) {
	body := []byte(`{
		"plan_type": "plus",
		"rate_limit": {
			"allowed": true,
			"limit_reached": false,
			"primary_window": {"used_percent": 0, "limit_window_seconds": 18000, "reset_after_seconds": 18000, "reset_at": 4102444800},
			"secondary_window": null
		}
	}`)
	snap, err := Lookup(FormatChatGPT).Parse(body)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	m := snap.Models[0]
	if m.IntervalUsedPct != 0 {
		t.Errorf("interval_used_percent=%d want 0", m.IntervalUsedPct)
	}
	if m.IntervalEndTime != nil || m.IntervalRemainsMs != 0 {
		t.Errorf("zero-percent window must drop reset_at, got end=%v remains=%d", m.IntervalEndTime, m.IntervalRemainsMs)
	}
}

// An unrecognized payload (no plan_type and no primary window) must fail
// loudly instead of rendering an empty card.
func TestChatGPTParser_UnexpectedShape(t *testing.T) {
	for _, body := range []string{`{}`, `{"rate_limit":{}}`, `{"usage":{"percent":1}}`} {
		if _, err := Lookup(FormatChatGPT).Parse([]byte(body)); err == nil {
			t.Errorf("expected error for shape %s", body)
		}
	}
}

func TestChatGPTParser_InvalidJSON(t *testing.T) {
	if _, err := Lookup(FormatChatGPT).Parse([]byte("not json")); err == nil {
		t.Fatal("expected error for invalid json")
	}
}

func TestChatGPTTokenExp(t *testing.T) {
	// JWT with payload {"exp":4102444800} — header/signature content irrelevant.
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":4102444800}`))
	token := "xxx." + payload + ".yyy"
	exp, ok := chatGPTTokenExp(token)
	if !ok {
		t.Fatal("expected ok for well-formed JWT")
	}
	if exp.Unix() != 4102444800 {
		t.Errorf("exp=%d want 4102444800", exp.Unix())
	}
	for _, bad := range []string{"", "not-a-jwt", "a.b", "a.!!!.c"} {
		if _, ok := chatGPTTokenExp(bad); ok {
			t.Errorf("expected not-ok for %q", bad)
		}
	}
}

func TestRegistry_UnknownFormat(t *testing.T) {
	if Lookup("nope") != nil {
		t.Error("expected nil for unknown format")
	}
	if Lookup("") != nil {
		t.Error("expected nil for empty format")
	}
}
