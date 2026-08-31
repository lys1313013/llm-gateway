package quota

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// openCodeGoResponse mirrors the JSON returned by
// https://opencode.ai/zen/go/v1/usage (first-party but undocumented):
//
//	{"usage":{"rolling"|"weekly"|"monthly":
//	    {"status":"ok"|"rate-limited","percent":0-100,"resetsAt":"ISO8601"}}}
//
// The three windows correspond to the plan's $12/5h, $30/week, $60/month
// dollar caps; the endpoint reports percentages only, no amounts.
type openCodeGoResponse struct {
	Usage map[string]openCodeGoWindow `json:"usage"`
}

type openCodeGoWindow struct {
	Status   string  `json:"status"`
	Percent  float64 `json:"percent"`
	ResetsAt string  `json:"resetsAt"`
}

type openCodeGoParser struct{}

func (openCodeGoParser) Format() string { return FormatOpenCodeGo }

func (openCodeGoParser) Parse(body []byte) (Snapshot, error) {
	var resp openCodeGoResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return Snapshot{}, fmt.Errorf("opencode_go: invalid json: %w", err)
	}

	now := time.Now()
	m := ModelQuota{
		ModelName:  "opencode-go",
		Status:     1,
		StatusText: "使用中",
	}
	found := false
	if w, ok := resp.Usage["rolling"]; ok {
		m.IntervalUsedPct, m.IntervalRemainsMs, m.IntervalEndTime = openCodeGoCycle(w, now)
		found = true
	}
	if w, ok := resp.Usage["weekly"]; ok {
		m.WeeklyUsedPct, m.WeeklyRemainsMs, m.WeeklyEndTime = openCodeGoCycle(w, now)
		found = true
	}
	if w, ok := resp.Usage["monthly"]; ok {
		pct, remainsMs, endTime := openCodeGoCycle(w, now)
		m.MonthlyUsedPct = &pct
		m.MonthlyRemainsMs = remainsMs
		m.MonthlyEndTime = endTime
		found = true
	}
	// All three windows missing = the (undocumented) response shape changed
	// again — fail loudly instead of rendering an empty card.
	if !found {
		return Snapshot{}, fmt.Errorf("opencode_go: unexpected usage response shape")
	}

	return Snapshot{
		DisplayType: DisplayTypeModelRemains,
		Models:      []ModelQuota{m},
		FetchedAt:   now,
	}, nil
}

// openCodeGoCycle converts one window into display stats. percent is the
// upstream's used-percent integer; status=="rate-limited" already arrives as
// percent=100, so no special-casing. When percent is 0 the upstream resetsAt
// is a "now+window" placeholder (the rolling window cleared long ago), so it
// is dropped rather than shown as a countdown.
func openCodeGoCycle(w openCodeGoWindow, now time.Time) (usedPct int, remainsMs int64, endTime *time.Time) {
	usedPct = int(math.Floor(w.Percent))
	if usedPct < 0 {
		usedPct = 0
	}
	if usedPct > 100 {
		usedPct = 100
	}
	if usedPct == 0 {
		return usedPct, 0, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, w.ResetsAt); err == nil {
		endTime = &t
		if d := t.Sub(now); d > 0 {
			remainsMs = d.Milliseconds()
		}
	}
	return usedPct, remainsMs, endTime
}

func init() {
	Register(openCodeGoParser{})
}
