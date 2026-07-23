package quota

import (
	"encoding/json"
	"fmt"
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
func kimiCycle(u kimiUsage, now time.Time) (s kimiCycleStat, ok bool) {
	s.used, _ = strconv.ParseInt(u.Used, 10, 64)
	s.limit, _ = strconv.ParseInt(u.Limit, 10, 64)
	if s.limit > 0 {
		s.usedPct = int(s.used * 100 / s.limit)
	}
	if t, err := time.Parse(time.RFC3339Nano, u.ResetTime); err == nil {
		s.resetAt = &t
		if d := t.Sub(now); d > 0 {
			s.remainsMs = d.Milliseconds()
		}
	}
	return s, u.Limit != "" || u.Used != ""
}

func init() {
	Register(kimiParser{})
}
