package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/lys1313013/llm-gateway/backend-go/internal/db"
	"github.com/lys1313013/llm-gateway/backend-go/internal/models"
	"github.com/lys1313013/llm-gateway/backend-go/internal/token"
)

// ---------------------------------------------------------------------------
// Anthropic
// ---------------------------------------------------------------------------

func HandleAnthropic(ctx context.Context, requestData []byte, cfg models.ProxyConfig) (int, http.Header, io.ReadCloser, bool, error) {
	start := time.Now()

	patched, err := patchModelField(requestData, cfg.Model)
	if err != nil {
		return 0, nil, nil, false, err
	}

	timeout := timeoutFromConfig(cfg.Timeout)

	httpClient := &http.Client{
		Timeout:   timeout,
		Transport: streamingTransport,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TargetURL, strings.NewReader(string(patched)))
	if err != nil {
		return 0, nil, nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", cfg.APIKey)
	req.Header.Set("anthropic-version", cfg.AnthropicVersion)

	if cfg.LogRequests {
		slog.Info("anthropic proxy: forwarding", "url", cfg.TargetURL, "model", cfg.Model)
	}

	var probe map[string]any
	_ = json.Unmarshal(patched, &probe)
	isStream, _ := probe["stream"].(bool)

	resp, err := httpClient.Do(req)
	if err != nil {
		status := 502
		if isTimeoutErr(err) {
			status = 504
		}
		logProxyError(ctx, requestData, cfg, start, status, err.Error())
		return status, nil, nil, false, nil
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		var parsed any
		_ = json.Unmarshal(body, &parsed)
		var respData any = parsed
		if _, ok := parsed.(map[string]any); !ok {
			respData = map[string]any{"raw": string(body)}
		}
		respBytes, _ := json.Marshal(respData)

		_ = db.InsertLog(ctx, db.InsertLogInput{
			Model:            strPtr(modelFromRequest(probe, cfg.Model)),
			IsStream:         isStream,
			StatusCode:       resp.StatusCode,
			ProcessingTimeMs: int(time.Since(start).Milliseconds()),
			TargetURL:        strPtr(cfg.TargetURL),
			RequestData:      requestData,
			ResponseData:     respBytes,
			ErrorMessage:     strPtr(fmt.Sprintf("Target API returned error: %d", resp.StatusCode)),
			Protocol:         strPtr(cfg.Protocol),
		})

		// Return Anthropic-format error
		errBody, _ := json.Marshal(map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    "api_error",
				"message": fmt.Sprintf("Upstream returned status %d", resp.StatusCode),
			},
		})
		// If the upstream already returned an Anthropic-shaped error, prefer it
		if _, ok := parsed.(map[string]any); ok {
			if m, ok := parsed.(map[string]any); ok {
				if _, hasErr := m["error"]; hasErr {
					errBody = body
				}
			}
		}
		return resp.StatusCode, nil, io.NopCloser(strings.NewReader(string(errBody))), false, nil
	}

	if isStream {
		streamer := &anthropicStreamer{
			resp:         resp.Body,
			logResponses: cfg.LogResponses,
			targetURL:    cfg.TargetURL,
			requestData:  requestData,
			protocol:     cfg.Protocol,
			start:        start,
			model:        modelFromRequest(probe, cfg.Model),
		}
		return resp.StatusCode, resp.Header, streamer, true, nil
	}

	// Non-streaming
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	var parsed map[string]any
	_ = json.Unmarshal(body, &parsed)
	usageBytes, _ := json.Marshal(map[string]any{})
	if u, ok := parsed["usage"].(map[string]any); ok {
		usageBytes, _ = json.Marshal(u)
	}
	norm := token.NormalizeUsage(usageBytes)
	if norm == nil {
		n := token.CalculateAnthropicUsage(probe, parsed)
		norm = &n
	}

	_ = db.InsertLog(ctx, db.InsertLogInput{
		Model:                    strPtr(modelFromRequest(probe, cfg.Model)),
		IsStream:                 false,
		StatusCode:               resp.StatusCode,
		ProcessingTimeMs:         int(time.Since(start).Milliseconds()),
		PromptTokens:             intPtr(norm.PromptTokens),
		CompletionTokens:         intPtr(norm.CompletionTokens),
		TotalTokens:              intPtr(norm.TotalTokens),
		CacheCreationInputTokens: intPtr(norm.CacheCreationInputTokens),
		CacheReadInputTokens:     intPtr(norm.CacheReadInputTokens),
		TargetURL:                strPtr(cfg.TargetURL),
		RequestData:              requestData,
		ResponseData:             body,
		Protocol:                 strPtr(cfg.Protocol),
		UsageData:                norm.Raw,
	})

	return resp.StatusCode, nil, io.NopCloser(strings.NewReader(string(body))), false, nil
}

type anthropicStreamer struct {
	resp         io.ReadCloser
	logResponses bool
	targetURL    string
	requestData  []byte
	protocol     string
	start        time.Time
	model        string

	pending  []byte
	finished bool
}

func (s *anthropicStreamer) Read(p []byte) (int, error) {
	if s.finished {
		return 0, io.EOF
	}
	n, err := s.resp.Read(p)
	if n > 0 {
		s.pending = append(s.pending, p[:n]...)
	}
	if err == io.EOF {
		s.finished = true
		s.finalize()
	}
	return n, err
}

func (s *anthropicStreamer) Close() error {
	if !s.finished {
		s.finished = true
		s.finalize()
	}
	return s.resp.Close()
}

func (s *anthropicStreamer) finalize() {
	if !s.logResponses {
		return
	}
	events := s.collectEvents()
	agg := aggregateAnthropicStream(events)
	aggJSON, _ := json.Marshal(agg)

	usageBytes, _ := json.Marshal(agg["usage"])
	norm := token.NormalizeUsage(usageBytes)
	if norm == nil {
		var req any
		_ = json.Unmarshal(s.requestData, &req)
		n := token.CalculateAnthropicUsage(req, agg)
		norm = &n
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = db.InsertLog(ctx, db.InsertLogInput{
		Model:                    strPtr(s.model),
		IsStream:                 true,
		StatusCode:               http.StatusOK,
		ProcessingTimeMs:         int(time.Since(s.start).Milliseconds()),
		PromptTokens:             intPtr(norm.PromptTokens),
		CompletionTokens:         intPtr(norm.CompletionTokens),
		TotalTokens:              intPtr(norm.TotalTokens),
		CacheCreationInputTokens: intPtr(norm.CacheCreationInputTokens),
		CacheReadInputTokens:     intPtr(norm.CacheReadInputTokens),
		TargetURL:                strPtr(s.targetURL),
		RequestData:              s.requestData,
		ResponseData:             aggJSON,
		Protocol:                 strPtr(s.protocol),
		UsageData:                norm.Raw,
	})
}

func (s *anthropicStreamer) collectEvents() []map[string]any {
	var out []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(string(s.pending)))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		var ev map[string]any
		if err := json.Unmarshal([]byte(payload), &ev); err == nil {
			out = append(out, ev)
		}
	}
	return out
}

// aggregateAnthropicStream mirrors _aggregate_anthropic_stream_events in
// anthropic_proxy.py.
func aggregateAnthropicStream(events []map[string]any) map[string]any {
	result := map[string]any{
		"id":         nil,
		"type":       "message",
		"role":       "assistant",
		"model":      nil,
		"content":    []any{},
		"stop_reason": nil,
		"usage": map[string]any{
			"input_tokens":                 0,
			"output_tokens":                0,
			"cache_creation_input_tokens": 0,
			"cache_read_input_tokens":     0,
		},
	}
	blocks := map[int]map[string]any{}

	for _, ev := range events {
		switch ev["type"] {
		case "message_start":
			msg, _ := ev["message"].(map[string]any)
			if msg != nil {
				if v, ok := msg["id"]; ok {
					result["id"] = v
				}
				if v, ok := msg["model"]; ok {
					result["model"] = v
				}
				if v, ok := msg["role"]; ok {
					result["role"] = v
				}
				if u, ok := msg["usage"].(map[string]any); ok {
					usage, _ := result["usage"].(map[string]any)
					usage["input_tokens"] = asFloat(u["input_tokens"])
					usage["output_tokens"] = asFloat(u["output_tokens"])
					usage["cache_creation_input_tokens"] = asFloat(u["cache_creation_input_tokens"])
					usage["cache_read_input_tokens"] = asFloat(u["cache_read_input_tokens"])
				}
			}
		case "content_block_start":
			idx := int(asFloat(ev["index"]))
			cb, _ := ev["content_block"].(map[string]any)
			if cb == nil {
				cb = map[string]any{}
			}
			blocks[idx] = copyMap(cb)
		case "content_block_delta":
			idx := int(asFloat(ev["index"]))
			delta, _ := ev["delta"].(map[string]any)
			if _, ok := blocks[idx]; !ok {
				blocks[idx] = map[string]any{}
			}
			cb := blocks[idx]
			switch delta["type"] {
			case "text_delta":
				cb["type"] = "text"
				cur, _ := cb["text"].(string)
				if t, ok := delta["text"].(string); ok {
					cb["text"] = cur + t
				}
			case "input_json_delta":
				cb["type"] = "tool_use"
				cur, _ := cb["input"].(string)
				if pj, ok := delta["partial_json"].(string); ok {
					cb["input"] = cur + pj
				}
			case "thinking_delta":
				cb["type"] = "thinking"
				cur, _ := cb["thinking"].(string)
				if t, ok := delta["thinking"].(string); ok {
					cb["thinking"] = cur + t
				}
			}
		case "content_block_stop":
			idx := int(asFloat(ev["index"]))
			if cb, ok := blocks[idx]; ok && cb["type"] == "tool_use" {
				raw, _ := cb["input"].(string)
				if raw == "" {
					raw = "{}"
				}
				var parsed any
				if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
					cb["input"] = parsed
				}
			}
		case "message_delta":
			delta, _ := ev["delta"].(map[string]any)
			if v, ok := delta["stop_reason"]; ok {
				result["stop_reason"] = v
			}
			if v, ok := delta["stop_sequence"]; ok {
				result["stop_sequence"] = v
			}
			if u, ok := ev["usage"].(map[string]any); ok {
				usage, _ := result["usage"].(map[string]any)
				if v, ok := u["output_tokens"]; ok {
					usage["output_tokens"] = asFloat(v)
				}
			}
		}
	}

	keys := make([]int, 0, len(blocks))
	for k := range blocks {
		keys = append(keys, k)
	}
	sortInts(keys)
	ordered := make([]any, 0, len(keys))
	for _, k := range keys {
		ordered = append(ordered, blocks[k])
	}
	result["content"] = ordered
	return result
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
