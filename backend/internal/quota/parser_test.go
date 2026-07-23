package quota

import (
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

func TestRegistry_UnknownFormat(t *testing.T) {
	if Lookup("nope") != nil {
		t.Error("expected nil for unknown format")
	}
	if Lookup("") != nil {
		t.Error("expected nil for empty format")
	}
}
