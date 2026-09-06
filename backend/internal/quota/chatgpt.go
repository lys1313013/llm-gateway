package quota

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/lys1313013/llm-gateway/backend/internal/db"
	"github.com/lys1313013/llm-gateway/backend/internal/models"
)

// chatGPTResponse mirrors the JSON returned by the undocumented
// https://chatgpt.com/backend-api/wham/usage endpoint that the official
// Codex CLI polls for ChatGPT Plus/Pro/Team subscription usage. Shape may
// change without notice; keep queries read-only.
//
//	{"plan_type":"plus",
//	 "rate_limit":{"allowed":true,"limit_reached":false,
//	   "primary_window":{"used_percent":45,"limit_window_seconds":18000,
//	                     "reset_after_seconds":1500,"reset_at":1783440000},
//	   "secondary_window":{...}}}
//
// primary_window is the ~5h session window, secondary_window the weekly one
// (null on some plans). Team accounts additionally nest a monthly credit
// limit under spend_control.individual_limit — not mapped yet.
type chatGPTResponse struct {
	PlanType  string `json:"plan_type"`
	RateLimit struct {
		Allowed         bool           `json:"allowed"`
		LimitReached    bool           `json:"limit_reached"`
		PrimaryWindow   *chatGPTWindow `json:"primary_window"`
		SecondaryWindow *chatGPTWindow `json:"secondary_window"`
	} `json:"rate_limit"`
}

type chatGPTWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
	ResetAfterSeconds  int64   `json:"reset_after_seconds"`
	ResetAt            int64   `json:"reset_at"`
}

type chatGPTParser struct{}

func (chatGPTParser) Format() string { return FormatChatGPT }

func (chatGPTParser) Parse(body []byte) (Snapshot, error) {
	var resp chatGPTResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return Snapshot{}, fmt.Errorf("chatgpt: invalid json: %w", err)
	}
	if resp.PlanType == "" && resp.RateLimit.PrimaryWindow == nil {
		return Snapshot{}, fmt.Errorf("chatgpt: unexpected usage response shape")
	}

	now := time.Now()
	m := ModelQuota{
		ModelName:  "ChatGPT " + resp.PlanType,
		Status:     1,
		StatusText: "使用中",
	}
	if resp.RateLimit.LimitReached {
		m.Status = 2
		m.StatusText = "已限流"
	}
	if w := resp.RateLimit.PrimaryWindow; w != nil {
		m.IntervalUsedPct, m.IntervalRemainsMs, m.IntervalEndTime = chatGPTCycle(*w, now)
		if w.LimitWindowSeconds > 0 {
			sec := w.LimitWindowSeconds
			m.IntervalWindowSeconds = &sec
		}
	}
	if w := resp.RateLimit.SecondaryWindow; w != nil {
		m.WeeklyUsedPct, m.WeeklyRemainsMs, m.WeeklyEndTime = chatGPTCycle(*w, now)
		if w.LimitWindowSeconds > 0 {
			sec := w.LimitWindowSeconds
			m.WeeklyWindowSeconds = &sec
		}
	}
	// Pro plans report only one (weekly-length) primary window — tell the UI
	// not to render a redundant second "本周" row.
	weeklyPresent := resp.RateLimit.SecondaryWindow != nil
	m.WeeklyPresent = &weeklyPresent

	return Snapshot{
		DisplayType: DisplayTypeModelRemains,
		Models:      []ModelQuota{m},
		FetchedAt:   now,
	}, nil
}

// chatGPTCycle converts one window into display stats. reset_at is unix
// seconds; when percent is 0 the window is idle and the reset timestamp is a
// placeholder, so it is dropped rather than shown as a countdown (same rule
// as opencode_go).
func chatGPTCycle(w chatGPTWindow, now time.Time) (usedPct int, remainsMs int64, endTime *time.Time) {
	usedPct = int(math.Floor(w.UsedPercent))
	if usedPct < 0 {
		usedPct = 0
	}
	if usedPct > 100 {
		usedPct = 100
	}
	if usedPct == 0 {
		return usedPct, 0, nil
	}
	if w.ResetAt > 0 {
		t := time.Unix(w.ResetAt, 0)
		endTime = &t
		if d := t.Sub(now); d > 0 {
			remainsMs = d.Milliseconds()
		}
	}
	return usedPct, remainsMs, endTime
}

// ---------------------------------------------------------------------------
// Token lifecycle + custom fetch (ProviderFetcher)
// ---------------------------------------------------------------------------

const (
	chatGPTRefreshURL = "https://auth.openai.com/oauth/token"
	// chatGPTClientID is the Codex CLI's public OAuth client id
	// (openai/codex codex-rs/login/src/auth/manager.rs CLIENT_ID).
	chatGPTClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	// chatGPTRefreshMargin refreshes proactively when the access token has
	// less than this much lifetime left.
	chatGPTRefreshMargin = 5 * time.Minute
)

type chatGPTRefreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// FetchProvider implements ProviderFetcher: the wham/usage endpoint needs a
// refreshable Codex OAuth access token rather than the static api_key, so
// this format takes over the whole request.
func (p chatGPTParser) FetchProvider(ctx context.Context, f *Fetcher, prov models.Provider) (Snapshot, error) {
	if prov.QuotaAccessToken == nil || *prov.QuotaAccessToken == "" ||
		prov.QuotaRefreshToken == nil || *prov.QuotaRefreshToken == "" {
		return Snapshot{}, fmt.Errorf("quota_access_token / quota_refresh_token 未配置")
	}
	if prov.QuotaURL == nil || *prov.QuotaURL == "" {
		return Snapshot{}, fmt.Errorf("quota_url 未配置")
	}

	// Serialize against concurrent refreshes of the same provider (background
	// refresher + manual refresh) so a rotated refresh token isn't clobbered.
	unlock := f.lockProvider(prov.ID)
	defer unlock()

	accessToken := *prov.QuotaAccessToken
	refreshToken := *prov.QuotaRefreshToken

	// Proactive refresh when the JWT is about to expire.
	if exp, ok := chatGPTTokenExp(accessToken); ok && time.Until(exp) < chatGPTRefreshMargin {
		var err error
		if accessToken, refreshToken, err = p.refreshTokens(ctx, f, prov.ID, refreshToken); err != nil {
			return Snapshot{}, err
		}
	}

	body, status, err := p.fetchUsage(ctx, f, *prov.QuotaURL, accessToken, prov.QuotaAccountID)
	if err != nil {
		return Snapshot{}, err
	}
	// Expired-but-not-detected token: refresh once and retry.
	if status == http.StatusUnauthorized {
		if accessToken, refreshToken, err = p.refreshTokens(ctx, f, prov.ID, refreshToken); err != nil {
			return Snapshot{}, err
		}
		body, status, err = p.fetchUsage(ctx, f, *prov.QuotaURL, accessToken, prov.QuotaAccountID)
		if err != nil {
			return Snapshot{}, err
		}
	}
	if status < 200 || status >= 300 {
		return Snapshot{}, fmt.Errorf("upstream status %d", status)
	}
	return p.Parse(body)
}

func (p chatGPTParser) fetchUsage(ctx context.Context, f *Fetcher, url, accessToken string, accountID *string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	if accountID != nil && *accountID != "" {
		req.Header.Set("ChatGPT-Account-Id", *accountID)
	}
	resp, err := f.HTTP.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	body, err := readAllLimited(resp.Body, 1<<20) // 1 MiB
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read body: %w", err)
	}
	return body, resp.StatusCode, nil
}

// refreshTokens exchanges the refresh token for a new access token at
// auth.openai.com (same flow as Codex CLI) and persists the result. The
// upstream may rotate the refresh token; when it doesn't, the old one stays.
func (p chatGPTParser) refreshTokens(ctx context.Context, f *Fetcher, providerID int, refreshToken string) (string, string, error) {
	reqBody, err := json.Marshal(map[string]string{
		"client_id":     chatGPTClientID,
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
	})
	if err != nil {
		return "", "", fmt.Errorf("marshal refresh request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chatGPTRefreshURL, strings.NewReader(string(reqBody)))
	if err != nil {
		return "", "", fmt.Errorf("build refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.HTTP.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("refresh http: %w", err)
	}
	defer resp.Body.Close()
	body, err := readAllLimited(resp.Body, 1<<20)
	if err != nil {
		return "", "", fmt.Errorf("read refresh body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("token refresh failed (status %d)，请重新粘贴 auth.json 里的 token", resp.StatusCode)
	}
	var rr chatGPTRefreshResponse
	if err := json.Unmarshal(body, &rr); err != nil {
		return "", "", fmt.Errorf("refresh: invalid json: %w", err)
	}
	if rr.AccessToken == "" {
		return "", "", fmt.Errorf("token refresh returned no access_token")
	}
	if rr.RefreshToken != "" {
		refreshToken = rr.RefreshToken
	}
	if err := db.UpdateProviderQuotaTokens(ctx, providerID, rr.AccessToken, refreshToken); err != nil {
		return "", "", fmt.Errorf("persist refreshed tokens: %w", err)
	}
	return rr.AccessToken, refreshToken, nil
}

// chatGPTTokenExp extracts the exp claim from a JWT without verifying the
// signature (the token is only ever sent back to its issuer).
func chatGPTTokenExp(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0), true
}

func init() {
	Register(chatGPTParser{})
}
