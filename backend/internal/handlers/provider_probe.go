package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lys1313013/llm-gateway/backend/internal/middleware"
	"github.com/lys1313013/llm-gateway/backend/internal/models"
)

// ---------------------------------------------------------------------------
// Provider one-shot connectivity test — let the admin probe a provider
// config (OpenAI / Anthropic base URL + API key) WITHOUT persisting it
// first. The handler auto-detects which protocol(s) are configured and
// runs an end-to-end chat for each:
//
//   * OpenAI  — list models, pick the first, send a one-token "hi" chat.
//   * Anthropic — send a one-token "hi" chat against a best-guess default
//     model. Anthropic has no public models endpoint, so we can't list;
//     the default may not exist on every provider, and a 404 from a
//     reachable host still proves the URL + key are wired up correctly.
//
// No DB writes, no api_logs insertion, no model_route lookup.
// ---------------------------------------------------------------------------

// TestProviderInput is the body for /api/provider/test/connect. The
// frontend sends the same shape the form holds, so the test works on
// unsaved configurations.
type TestProviderInput struct {
	OpenAIBaseURL    *string `json:"openai_base_url"`
	AnthropicBaseURL *string `json:"anthropic_base_url"`
	ResponsesBaseURL *string `json:"responses_base_url"`
	APIKey           *string `json:"api_key"`
}

// ProtocolTestResult is the per-protocol outcome returned to the UI.
type ProtocolTestResult struct {
	OK       bool   `json:"ok"`
	Model    string `json:"model,omitempty"`
	LatencyMs int64 `json:"latency_ms"`
	Status   int    `json:"status,omitempty"`
	Response string `json:"response,omitempty"`
	Error    string `json:"error,omitempty"`
}

// defaultAnthropicModel is a best-effort guess for Anthropic-compatible
// providers that don't expose a /v1/models endpoint. Common alternatives
// (claude-3-5-sonnet-latest, claude-3-opus-20240229) are tried in order.
var defaultAnthropicModels = []string{
	"claude-sonnet-4-5",
	"claude-3-5-sonnet-latest",
	"claude-3-5-sonnet-20241022",
	"claude-3-opus-20240229",
}

// defaultResponsesModel is a best-effort guess for Responses-API-compatible
// providers. OpenAI's own endpoint uses gpt-4o / gpt-4o-mini; DashScope
// (阿里云百炼) accepts qwen-plus / qwen-turbo. We try a small list.
var defaultResponsesModels = []string{
	"gpt-4o-mini",
	"gpt-4o",
	"qwen-plus",
	"qwen-turbo",
}

// ProviderConnect runs the end-to-end test and returns one result per
// configured protocol. If both URLs are set, both are tested in parallel
// and both are returned.
func ProviderConnect(c *gin.Context) {
	middleware.RequireAdmin(c)
	if c.IsAborted() {
		return
	}
	var in TestProviderInput
	if !bindJSON(c, &in) {
		return
	}
	if in.APIKey == nil || *in.APIKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "api_key is required",
		})
		return
	}

	hasOpenAI := in.OpenAIBaseURL != nil && *in.OpenAIBaseURL != ""
	hasAnthropic := in.AnthropicBaseURL != nil && *in.AnthropicBaseURL != ""
	hasResponses := in.ResponsesBaseURL != nil && *in.ResponsesBaseURL != ""
	if !hasOpenAI && !hasAnthropic && !hasResponses {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请至少填写一个协议的 Base URL",
		})
		return
	}

	type slot struct {
		name   string
		result ProtocolTestResult
	}
	results := make(map[string]ProtocolTestResult)

	// Run sequentially to keep the handler simple — these are quick calls.
	if hasOpenAI {
		results["openai"] = runOpenAITest(c.Request.Context(), *in.OpenAIBaseURL, *in.APIKey)
	}
	if hasAnthropic {
		results["anthropic"] = runAnthropicTest(c.Request.Context(), *in.AnthropicBaseURL, *in.APIKey)
	}
	if hasResponses {
		results["responses"] = runResponsesTest(c.Request.Context(), *in.ResponsesBaseURL, *in.APIKey)
	}

	ok(c, gin.H{"results": results})
}

// ---------------------------------------------------------------------------
// OpenAI: list models → pick first → chat "hi"
// ---------------------------------------------------------------------------

func runOpenAITest(ctx context.Context, base, apiKey string) ProtocolTestResult {
	start := time.Now()
	ids, err := fetchOpenAIModels(ctx, base, apiKey)
	if err != nil {
		return ProtocolTestResult{
			OK:        false,
			LatencyMs: time.Since(start).Milliseconds(),
			Error:     "拉取模型列表失败: " + err.Error(),
		}
	}
	if len(ids) == 0 {
		return ProtocolTestResult{
			OK:        false,
			LatencyMs: time.Since(start).Milliseconds(),
			Error:     "上游返回的模型列表为空",
		}
	}
	model := ids[0]
	chatStart := time.Now()
	body := buildOpenAIBody(model, "hi", 32)
	cfg := models.ProxyConfig{
		TargetURL: buildOpenAITargetURL(&base),
		APIKey:    apiKey,
		Timeout:   60,
		Model:     model,
		Protocol:  "openai",
	}
	status, respBody, err := forwardTestRequest(ctx, body, cfg)
	if err != nil {
		return ProtocolTestResult{
			OK:        false,
			Model:     model,
			LatencyMs: time.Since(start).Milliseconds(),
			Error:     err.Error(),
		}
	}
	if status != http.StatusOK {
		return ProtocolTestResult{
			OK:        false,
			Model:     model,
			LatencyMs: time.Since(start).Milliseconds(),
			Status:    status,
			Error:     extractUpstreamError(respBody, status),
		}
	}
	content := extractOpenAIContent(respBody)
	return ProtocolTestResult{
		OK:        content != "",
		Model:     model,
		LatencyMs: time.Since(chatStart).Milliseconds(),
		Status:    status,
		Response:  content,
	}
}

func fetchOpenAIModels(ctx context.Context, base, apiKey string) ([]string, error) {
	b := strings.TrimRight(base, "/")
	if strings.HasSuffix(b, "/chat/completions") {
		b = strings.TrimSuffix(b, "/chat/completions")
	}
	if !strings.HasSuffix(b, "/v1") {
		b += "/v1"
	}
	url := b + "/models"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		snippet := string(body)
		if len(snippet) > 300 {
			snippet = snippet[:300] + "..."
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet)
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("解析失败: %w", err)
	}
	ids := make([]string, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}

// ---------------------------------------------------------------------------
// Anthropic: chat "hi" with the first model that returns 2xx
// ---------------------------------------------------------------------------

func runAnthropicTest(ctx context.Context, base, apiKey string) ProtocolTestResult {
	start := time.Now()
	target := buildAnthropicTargetURL(base)
	for _, model := range defaultAnthropicModels {
		body := buildAnthropicBody(model, "hi", 32)
		cfg := models.ProxyConfig{
			TargetURL:        target,
			APIKey:           apiKey,
			Timeout:          60,
			Model:            model,
			Protocol:         "anthropic",
			AnthropicVersion: "2023-06-01",
		}
		status, respBody, err := forwardTestRequest(ctx, body, cfg)
		if err != nil {
			// Network / timeout — try the next model with the same error
			// only if it looks like a model-not-found (404). For other
			// failures, surface the first one to the user.
			if status == 0 {
				return ProtocolTestResult{
					OK:        false,
					Model:     model,
					LatencyMs: time.Since(start).Milliseconds(),
					Error:     err.Error(),
				}
			}
			continue
		}
		if status == http.StatusOK {
			content := extractAnthropicContent(respBody)
			return ProtocolTestResult{
				OK:        content != "",
				Model:     model,
				LatencyMs: time.Since(start).Milliseconds(),
				Status:    status,
				Response:  content,
			}
		}
		if status == http.StatusNotFound {
			// Try the next default model.
			continue
		}
		// 401/403/500 — fail fast; the user has a real config problem.
		return ProtocolTestResult{
			OK:        false,
			Model:     model,
			LatencyMs: time.Since(start).Milliseconds(),
			Status:    status,
			Error:     extractUpstreamError(respBody, status),
		}
	}
	return ProtocolTestResult{
		OK:        false,
		LatencyMs: time.Since(start).Milliseconds(),
		Error:     "尝试所有默认模型均失败（Anthropic 协议无公开 models 端点，请确认该厂商支持的模型名）",
	}
}

// ---------------------------------------------------------------------------
// Responses API: chat "hi" with the first model that returns 2xx
// ---------------------------------------------------------------------------

func runResponsesTest(ctx context.Context, base, apiKey string) ProtocolTestResult {
	start := time.Now()
	target := buildResponsesTargetURL(&base)
	for _, model := range defaultResponsesModels {
		body := buildResponsesBody(model, "hi")
		cfg := models.ProxyConfig{
			TargetURL: target,
			APIKey:    apiKey,
			Timeout:   60,
			Model:     model,
			Protocol:  "responses",
		}
		status, respBody, err := forwardTestRequest(ctx, body, cfg)
		if err != nil {
			if status == 0 {
				return ProtocolTestResult{
					OK:        false,
					Model:     model,
					LatencyMs: time.Since(start).Milliseconds(),
					Error:     err.Error(),
				}
			}
			continue
		}
		if status == http.StatusOK {
			content := extractResponsesContent(respBody)
			return ProtocolTestResult{
				OK:        content != "",
				Model:     model,
				LatencyMs: time.Since(start).Milliseconds(),
				Status:    status,
				Response:  content,
			}
		}
		if status == http.StatusNotFound {
			continue
		}
		return ProtocolTestResult{
			OK:        false,
			Model:     model,
			LatencyMs: time.Since(start).Milliseconds(),
			Status:    status,
			Error:     extractUpstreamError(respBody, status),
		}
	}
	return ProtocolTestResult{
		OK:        false,
		LatencyMs: time.Since(start).Milliseconds(),
		Error:     "尝试所有默认模型均失败（Responses 协议无公开 models 端点，请确认该厂商支持的模型名）",
	}
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

func buildAnthropicTargetURL(base string) string {
	return strings.TrimRight(base, "/") + "/v1/messages"
}

func buildOpenAIBody(model, userContent string, maxTokens int) []byte {
	p := map[string]any{
		"model":    model,
		"messages": []map[string]any{{"role": "user", "content": userContent}},
	}
	if maxTokens > 0 {
		p["max_tokens"] = maxTokens
	}
	b, _ := json.Marshal(p)
	return b
}

func buildAnthropicBody(model, userContent string, maxTokens int) []byte {
	p := map[string]any{
		"model":      model,
		"messages":   []map[string]any{{"role": "user", "content": userContent}},
		"max_tokens": maxTokens,
	}
	b, _ := json.Marshal(p)
	return b
}

func buildResponsesBody(model, userContent string) []byte {
	p := map[string]any{
		"model": model,
		"input": []map[string]any{{"role": "user", "content": userContent}},
	}
	b, _ := json.Marshal(p)
	return b
}

func extractOpenAIContent(body []byte) string {
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	if len(parsed.Choices) == 0 {
		return ""
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content)
}

func extractAnthropicContent(body []byte) string {
	var parsed struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	for _, b := range parsed.Content {
		if b.Text != "" {
			return strings.TrimSpace(b.Text)
		}
	}
	return ""
}

// extractResponsesContent walks the Responses API response shape:
//
//	{
//	  "output": [
//	    { "type": "message", "content": [ { "type": "output_text", "text": "..." } ] }
//	  ]
//	}
func extractResponsesContent(body []byte) string {
	var parsed struct {
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	var parts []string
	for _, item := range parsed.Output {
		if item.Type != "message" {
			continue
		}
		for _, c := range item.Content {
			if c.Text != "" {
				parts = append(parts, c.Text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, ""))
}

func extractUpstreamError(body []byte, status int) string {
	if len(body) == 0 {
		return fmt.Sprintf("HTTP %d（无响应体）", status)
	}
	// Try the OpenAI shape first, then Anthropic.
	var openai struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &openai); err == nil && openai.Error.Message != "" {
		return fmt.Sprintf("HTTP %d: %s", status, openai.Error.Message)
	}
	var anthropic struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &anthropic); err == nil && anthropic.Error.Message != "" {
		return fmt.Sprintf("HTTP %d: %s", status, anthropic.Error.Message)
	}
	snippet := string(body)
	if len(snippet) > 300 {
		snippet = snippet[:300] + "..."
	}
	return fmt.Sprintf("HTTP %d: %s", status, snippet)
}

func forwardTestRequest(ctx context.Context, body []byte, cfg models.ProxyConfig) (int, []byte, error) {
	timeout := 60 * time.Second
	if cfg.Timeout > 0 {
		timeout = time.Duration(cfg.Timeout) * time.Second
	}
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TargetURL, strings.NewReader(string(body)))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.Protocol == "openai" || cfg.Protocol == "responses" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	} else {
		req.Header.Set("x-api-key", cfg.APIKey)
		req.Header.Set("anthropic-version", cfg.AnthropicVersion)
	}
	slog.Info("provider test: forwarding",
		"url", cfg.TargetURL, "model", cfg.Model, "protocol", cfg.Protocol)

	resp, err := client.Do(req)
	if err != nil {
		return http.StatusBadGateway, nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, respBody, nil
}
