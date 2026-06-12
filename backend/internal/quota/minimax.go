package quota

import (
	"encoding/json"
	"fmt"
	"time"
)

// minimaxResponse mirrors the JSON returned by
// https://www.minimaxi.com/v1/api/openplatform/coding_plan/remains
type minimaxResponse struct {
	BaseResp struct {
		StatusCode int    `json:"status_code"`
		StatusMsg  string `json:"status_msg"`
	} `json:"base_resp"`
	ModelRemains []minimaxModel `json:"model_remains"`
}

type minimaxModel struct {
	ModelName string `json:"model_name"`

	CurrentIntervalStatus           int   `json:"current_interval_status"`
	CurrentIntervalUsageCount       int64 `json:"current_interval_usage_count"`
	CurrentIntervalTotalCount       int64 `json:"current_interval_total_count"`
	CurrentIntervalRemainingPercent int   `json:"current_interval_remaining_percent"`
	RemainsTime                     int64 `json:"remains_time"`
	StartTime                       int64 `json:"start_time"`
	EndTime                         int64 `json:"end_time"`

	CurrentWeeklyStatus           int   `json:"current_weekly_status"`
	CurrentWeeklyUsageCount       int64 `json:"current_weekly_usage_count"`
	CurrentWeeklyTotalCount       int64 `json:"current_weekly_total_count"`
	CurrentWeeklyRemainingPercent int   `json:"current_weekly_remaining_percent"`
	WeeklyRemainsTime             int64 `json:"weekly_remains_time"`
	WeeklyStartTime               int64 `json:"weekly_start_time"`
	WeeklyEndTime                 int64 `json:"weekly_end_time"`
}

type minimaxParser struct{}

func (minimaxParser) Format() string { return FormatMiniMax }

func (minimaxParser) Parse(body []byte) (Snapshot, error) {
	var resp minimaxResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return Snapshot{}, fmt.Errorf("minimax: invalid json: %w", err)
	}
	if resp.BaseResp.StatusCode != 0 {
		return Snapshot{}, fmt.Errorf("minimax: %s", resp.BaseResp.StatusMsg)
	}

	out := Snapshot{
		DisplayType: DisplayTypeModelRemains,
		Models:      make([]ModelQuota, 0, len(resp.ModelRemains)),
		FetchedAt:   time.Now(),
	}
	for _, m := range resp.ModelRemains {
		out.Models = append(out.Models, ModelQuota{
			ModelName:          m.ModelName,
			Status:             firstNonZero(m.CurrentIntervalStatus, m.CurrentWeeklyStatus),
			StatusText:         minimaxStatusText(firstNonZero(m.CurrentIntervalStatus, m.CurrentWeeklyStatus)),
			IntervalUsageCount: ptrInt64(m.CurrentIntervalUsageCount),
			IntervalTotalCount: ptrInt64(m.CurrentIntervalTotalCount),
			IntervalUsedPct:    usedPct(m.CurrentIntervalRemainingPercent),
			IntervalRemainsMs:  m.RemainsTime,
			IntervalStartTime:  tsPtr(m.StartTime),
			IntervalEndTime:    tsPtr(m.EndTime),
			WeeklyUsageCount:   ptrInt64(m.CurrentWeeklyUsageCount),
			WeeklyTotalCount:   ptrInt64(m.CurrentWeeklyTotalCount),
			WeeklyUsedPct:      usedPct(m.CurrentWeeklyRemainingPercent),
			WeeklyRemainsMs:    m.WeeklyRemainsTime,
			WeeklyStartTime:    tsPtr(m.WeeklyStartTime),
			WeeklyEndTime:      tsPtr(m.WeeklyEndTime),
		})
	}
	return out, nil
}

func minimaxStatusText(s int) string {
	switch s {
	case 1:
		return "使用中"
	case 2:
		return "警告"
	case 3:
		return "空闲"
	case 4:
		return "耗尽"
	default:
		return "未知"
	}
}

func usedPct(remaining int) int {
	if remaining <= 0 {
		return 100
	}
	if remaining >= 100 {
		return 0
	}
	return 100 - remaining
}

func ptrInt64(v int64) *int64 { return &v }

func tsPtr(ms int64) *time.Time {
	if ms <= 0 {
		return nil
	}
	t := time.UnixMilli(ms).UTC()
	return &t
}

func firstNonZero(a, b int) int {
	if a != 0 {
		return a
	}
	return b
}

func init() {
	Register(minimaxParser{})
}
