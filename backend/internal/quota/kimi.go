package quota

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"
)

// kimiResponse mirrors the JSON returned by
// https://api.kimi.com/coding/v1/usages
//
//	{
//	  "usage":  {"limit":"100","used":"34","remaining":"66","resetTime":"..."},
//	  "limits": [{"window":{"duration":300,"timeUnit":"TIME_UNIT_MINUTE"},
//	              "detail":{"limit":"100","used":"2","remaining":"98","resetTime":"..."}}]
//	}
//
// The top-level "usage" is the weekly-style cycle; entries under "limits"
// are short sliding windows (e.g. 5h). Both are rendered as one synthetic
// model row so the existing model_remains UI can display them.
type kimiResponse struct {
	Usage  kimiUsage `json:"usage"`
	Limits []struct {
		Detail kimiUsage `json:"detail"`
	} `json:"limits"`
}

type kimiUsage struct {
	Limit     string `json:"limit"`
	Used      string `json:"used"`
	Remaining string `json:"remaining"`
	ResetTime string `json:"resetTime"`
}

type kimiParser struct{}

func (kimiParser) Format() string { return FormatKimi }

func (kimiParser) Parse(body []byte) (Snapshot, error) {
	var resp kimiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return Snapshot{}, fmt.Errorf("kimi: invalid json: %w", err)
	}
	if resp.Usage.Limit == "" && len(resp.Limits) == 0 {
		return Snapshot{}, fmt.Errorf("kimi: empty usage payload")
	}

	now := time.Now()
	m := ModelQuota{
		ModelName:  "kimi-for-coding",
		Status:     1,
		StatusText: "使用中",
	}
	if u, ok := kimiCycle(resp.Usage, now); ok {
		m.WeeklyUsageCount = &u.used
		m.WeeklyTotalCount = &u.limit
		m.WeeklyUsedPct = u.usedPct
		m.WeeklyRemainsMs = u.remainsMs
		m.WeeklyEndTime = u.resetAt
	}
	if len(resp.Limits) > 0 {
		if u, ok := kimiCycle(resp.Limits[0].Detail, now); ok {
			m.IntervalUsageCount = &u.used
			m.IntervalTotalCount = &u.limit
			m.IntervalUsedPct = u.usedPct
			m.IntervalRemainsMs = u.remainsMs
			m.IntervalEndTime = u.resetAt
		}
	}

	return Snapshot{
		DisplayType: DisplayTypeModelRemains,
		Models:      []ModelQuota{m},
		FetchedAt:   now,
	}, nil
}

type kimiCycleStat struct {
	used, limit int64
	usedPct     int
	remainsMs   int64
	resetAt     *time.Time
}

// kimiCycle converts one usage/detail block (string counters + RFC3339
// reset time) into display-ready stats. ok is false when nothing parses.
//
// remaining is authoritative: Kimi's "used" is often absent and, near the
// boundary, rounded up to equal the limit, which would otherwise read as a
// false 100%. Counters may be fractional, so parse them as floats.
func kimiCycle(u kimiUsage, now time.Time) (s kimiCycleStat, ok bool) {
	limit, _ := strconv.ParseFloat(u.Limit, 64)
	used, _ := strconv.ParseFloat(u.Used, 64)
	remaining, _ := strconv.ParseFloat(u.Remaining, 64)
	if limit <= 0 {
		return s, false
	}
	// remaining is authoritative when present: Kimi's "used" can round up to
	// the limit near the boundary, producing a false 100%. When remaining is
	// absent, trust "used" verbatim — it may represent genuinely exhausted
	// quota (used == limit), which must not be masked.
	fromRemaining := false
	if u.Remaining != "" && remaining > 0 {
		used = limit - remaining
		fromRemaining = true
	}
	if used > limit {
		used = limit
	}

	s.limit = int64(limit)
	s.used = int64(math.Floor(used))
	s.usedPct = int(math.Floor(used / limit * 100))
	if s.usedPct > 100 {
		s.usedPct = 100
	}
	if t, err := time.Parse(time.RFC3339Nano, u.ResetTime); err == nil {
		s.resetAt = &t
		if d := t.Sub(now); d > 0 {
			s.remainsMs = d.Milliseconds()
		}
	}
	// Only apply the 99% cap when used was derived from an authoritative
	// remaining field. When remaining is absent and used==limit, the cycle
	// is genuinely exhausted — report 100%.
	if fromRemaining && s.usedPct >= 100 && s.remainsMs > 0 {
		s.usedPct = 99
	}
	return s, true
}

func init() {
	Register(kimiParser{})
}
