package quota

import (
	"encoding/json"
	"fmt"
	"time"
)

// deepseekResponse mirrors the JSON returned by
// https://api.deepseek.com/user/balance
type deepseekResponse struct {
	IsAvailable bool `json:"is_available"`
	BalanceInfos []struct {
		Currency         string `json:"currency"`
		TotalBalance     string `json:"total_balance"`
		GrantedBalance   string `json:"granted_balance"`
		ToppedUpBalance  string `json:"topped_up_balance"`
	} `json:"balance_infos"`
}

type deepseekParser struct{}

func (deepseekParser) Format() string { return FormatDeepSeek }

func (deepseekParser) Parse(body []byte) (Snapshot, error) {
	var resp deepseekResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return Snapshot{}, fmt.Errorf("deepseek: invalid json: %w", err)
	}
	if !resp.IsAvailable || len(resp.BalanceInfos) == 0 {
		return Snapshot{}, fmt.Errorf("deepseek: account unavailable or no balance info")
	}
	// Multi-currency: take the first entry (most common case is a single CNY/USD row).
	first := resp.BalanceInfos[0]
	return Snapshot{
		DisplayType: DisplayTypeBalance,
		Balance: &BalanceSnapshot{
			IsAvailable: resp.IsAvailable,
			Currency:    first.Currency,
			Total:       first.TotalBalance,
			Granted:     first.GrantedBalance,
			ToppedUp:    first.ToppedUpBalance,
		},
		FetchedAt: time.Now(),
	}, nil
}

func init() {
	Register(deepseekParser{})
}
