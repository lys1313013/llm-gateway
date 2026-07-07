// Package proxy implements the upstream LLM forwarding logic for both
// OpenAI and Anthropic protocols, including streaming SSE passthrough and
// aggregated response logging.
package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/lys1313013/llm-gateway/backend/internal/db"
	hdrpkg "github.com/lys1313013/llm-gateway/backend/internal/headers"
	"github.com/lys1313013/llm-gateway/backend/internal/models"
	"github.com/lys1313013/llm-gateway/backend/internal/token"
)

// ---------------------------------------------------------------------------
// OpenAI
// ---------------------------------------------------------------------------

// HandleOpenAI processes a request, returns a tuple (statusCode, headers,
// body, isStreaming, error). When isStreaming is true, the body is a stream
// that the handler must write to the wire.
func HandleOpenAI(ctx context.Context, requestData []byte, cfg models.ProxyConfig) (int, http.Header, io.ReadCloser, bool, error) {
	start := time.Now()

	// Patch the model in the JSON body if configured
	patched, err := patchModelField(requestData, cfg.Model)
	if err != nil {
		return 0, nil, nil, false, err
	}

	timeout := timeoutFromConfig(cfg.Timeout)

	httpClient := &http.Client{
		Timeout: timeout,
		Transport: streamingTransport,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TargetURL, strings.NewReader(string(patched)))
	if err != nil {
		return 0, nil, nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	if cfg.LogRequests {
		slog.Info("openai proxy: forwarding", "url", cfg.TargetURL, "model", cfg.Model)
	}

	// Determine if client asked for streaming
	var probe map[string]any
	_ = json.Unmarshal(patched, &probe)
	isStream, _ := probe["stream"].(bool)

	resp, err := httpClient.Do(req)
	if err != nil {
		// 504 timeout, 502 other
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
		_ = db.InsertLog(ctx, db.InsertLogInput{
			Model:          strPtr(modelFromRequest(probe, cfg.Model)),
			ProviderID:     cfg.ProviderID,
			ProviderName:   strPtrOrNil(cfg.ProviderName),
			IsStream:       isStream,
			StatusCode:     resp.StatusCode,
			ProcessingTimeMs: int(time.Since(start).Milliseconds()),
			TargetURL:      strPtr(cfg.TargetURL),
			RequestData:    requestData,
			ResponseData:   body,
			RequestHeaders: hdrpkg.ToJSON(cfg.RequestHeaders),
			ResponseHeaders: hdrpkg.ToJSON(hdrpkg.FromHTTPHeader(resp.Header)),
			ErrorMessage:   strPtr(fmt.Sprintf("Target API returned error: %d", resp.StatusCode)),
			Protocol:       strPtr(cfg.Protocol),
			SessionID:      strPtrOrNil(cfg.SessionID),
			UserID:         intPtrOrNil(cfg.UserID),
		})
		// Pass through the upstream error
		return resp.StatusCode, nil, io.NopCloser(strings.NewReader(string(body))), false, nil
	}

	if isStream {
		// Caller is responsible for logging the aggregated stream result
		// after the stream is fully consumed.
		streamer := &openaiStreamer{
			resp:            resp.Body,
			logResponses:    cfg.LogResponses,
			targetURL:       cfg.TargetURL,
			requestData:     requestData,
			protocol:        cfg.Protocol,
			providerID:      cfg.ProviderID,
			providerName:    strPtrOrNil(cfg.ProviderName),
			start:           start,
			model:           modelFromRequest(probe, cfg.Model),
			requestHeaders:  cfg.RequestHeaders,
			responseHeaders: hdrpkg.FromHTTPHeader(resp.Header),
			sessionID:       cfg.SessionID,
			userID:          cfg.UserID,
		}
		return resp.StatusCode, resp.Header, streamer, true, nil
	}

	// Non-streaming: read fully, log, return body
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return 502, nil, nil, false, nil
	}

	var parsed map[string]any
	_ = json.Unmarshal(body, &parsed)
	usageBytes, _ := json.Marshal(map[string]any{})
	if u, ok := parsed["usage"].(map[string]any); ok {
		usageBytes, _ = json.Marshal(u)
	}
	norm := token.NormalizeUsage(usageBytes)
	if norm == nil {
		n := token.CalculateOpenAIUsage(probe, parsed)
		norm = &n
	}

	_ = db.InsertLog(ctx, db.InsertLogInput{
		Model:                    strPtr(modelFromRequest(probe, cfg.Model)),
		ProviderID:               cfg.ProviderID,
		ProviderName:             strPtrOrNil(cfg.ProviderName),
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
		RequestHeaders:           hdrpkg.ToJSON(cfg.RequestHeaders),
		ResponseHeaders:          hdrpkg.ToJSON(hdrpkg.FromHTTPHeader(resp.Header)),
		Protocol:                 strPtr(cfg.Protocol),
		UsageData:                norm.Raw,
		SessionID:                strPtrOrNil(cfg.SessionID),
		UserID:                   intPtrOrNil(cfg.UserID),
	})

	return resp.StatusCode, nil, io.NopCloser(strings.NewReader(string(body))), false, nil
}

// openaiStreamer wraps the upstream SSE body so callers can read it like a
// normal http.Response.Body. When EOF is reached it inserts one api_log row
// with the aggregated stream contents.
type openaiStreamer struct {
	resp            io.ReadCloser
	logResponses    bool
	targetURL       string
	requestData     []byte
	protocol        string
	providerID      *int
	providerName    *string
	start           time.Time
	model           string
	requestHeaders  map[string]string
	responseHeaders map[string]string
	sessionID       string
	userID          int

	pending  []byte
	finished bool
}

func (s *openaiStreamer) Read(p []byte) (int, error) {
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

func (s *openaiStreamer) Close() error {
	if !s.finished {
		s.finished = true
		s.finalize()
	}
	return s.resp.Close()
}

func (s *openaiStreamer) finalize() {
	if !s.logResponses {
		return
	}
	chunks := s.collectChunks()
	agg := aggregateOpenAIStream(chunks)
	aggJSON, _ := json.Marshal(agg)

	usageBytes, _ := json.Marshal(agg["usage"])
	norm := token.NormalizeUsage(usageBytes)
	if norm == nil {
		var req any
		_ = json.Unmarshal(s.requestData, &req)
		n := token.CalculateOpenAIUsage(req, agg)
		norm = &n
	}

	// Use a fresh context because the request context may already be cancelled
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = db.InsertLog(ctx, db.InsertLogInput{
		Model:                    strPtr(s.model),
		ProviderID:               s.providerID,
		ProviderName:             s.providerName,
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
		RequestHeaders:           hdrpkg.ToJSON(s.requestHeaders),
		ResponseHeaders:          hdrpkg.ToJSON(s.responseHeaders),
		Protocol:                 strPtr(s.protocol),
		UsageData:                norm.Raw,
		SessionID:                strPtrOrNil(s.sessionID),
		UserID:                   intPtrOrNil(s.userID),
	})
}

func (s *openaiStreamer) collectChunks() []map[string]any {
	var out []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(string(s.pending)))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			continue
		}
		var chunk map[string]any
		if err := json.Unmarshal([]byte(payload), &chunk); err == nil {
			out = append(out, chunk)
		}
	}
	return out
}

// aggregateOpenAIStream mirrors _aggregate_stream_chunks in proxy.py.
func aggregateOpenAIStream(chunks []map[string]any) map[string]any {
	agg := map[string]any{
		"role":    "assistant",
		"content": "",
	}
	toolCalls := map[int]map[string]any{}
	functionCall := map[string]any{"name": "", "arguments": ""}
	hasFunctionCall := false
	var usage map[string]any

	for _, chunk := range chunks {
		if u, ok := chunk["usage"].(map[string]any); ok && u != nil {
			usage = u
		}
		choices, _ := chunk["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		choice, _ := choices[0].(map[string]any)
		delta, _ := choice["delta"].(map[string]any)
		if delta == nil {
			continue
		}

		if s, ok := delta["content"].(string); ok && s != "" {
			existing, _ := agg["content"].(string)
			agg["content"] = existing + s
		}
		if s, ok := delta["reasoning_content"].(string); ok && s != "" {
			existing, _ := agg["reasoning_content"].(string)
			agg["reasoning_content"] = existing + s
		}
		if fc, ok := delta["function_call"].(map[string]any); ok {
			hasFunctionCall = true
			if n, ok := fc["name"].(string); ok && n != "" {
				functionCall["name"] = functionCall["name"].(string) + n
			}
			if a, ok := fc["arguments"].(string); ok && a != "" {
				functionCall["arguments"] = functionCall["arguments"].(string) + a
			}
		}
		if tcs, ok := delta["tool_calls"].([]any); ok {
			for _, tcRaw := range tcs {
				tc, _ := tcRaw.(map[string]any)
				idx := int(asFloat(tc["index"]))
				if _, ok := toolCalls[idx]; !ok {
					toolCalls[idx] = map[string]any{
						"id":   "",
						"type": "function",
						"function": map[string]any{
							"name":      "",
							"arguments": "",
						},
					}
				}
				if v, ok := tc["id"].(string); ok && v != "" {
					toolCalls[idx]["id"] = v
				}
				if v, ok := tc["type"].(string); ok && v != "" {
					toolCalls[idx]["type"] = v
				}
				if fn, ok := tc["function"].(map[string]any); ok {
					inner, _ := toolCalls[idx]["function"].(map[string]any)
					if n, ok := fn["name"].(string); ok && n != "" {
						inner["name"] = inner["name"].(string) + n
					}
					if a, ok := fn["arguments"].(string); ok && a != "" {
						inner["arguments"] = inner["arguments"].(string) + a
					}
				}
			}
		}
	}

	if hasFunctionCall {
		agg["function_call"] = functionCall
	}
	if len(toolCalls) > 0 {
		keys := make([]int, 0, len(toolCalls))
		for k := range toolCalls {
			keys = append(keys, k)
		}
		sortInts(keys)
		ordered := make([]any, 0, len(keys))
		for _, k := range keys {
			ordered = append(ordered, toolCalls[k])
		}
		agg["tool_calls"] = ordered
	}
	if usage != nil {
		agg["usage"] = usage
	}
	return agg
}

func asFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	}
	return 0
}

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

// ---------------------------------------------------------------------------
// shared helpers
// ---------------------------------------------------------------------------

func patchModelField(body []byte, model string) ([]byte, error) {
	if model == "" {
		return body, nil
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	m["model"] = model
	return json.Marshal(m)
}

func modelFromRequest(probe map[string]any, fallback string) string {
	if m, ok := probe["model"].(string); ok && m != "" {
		return m
	}
	return fallback
}

func isTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr interface{ Timeout() bool }
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return false
}

func logProxyError(ctx context.Context, requestData []byte, cfg models.ProxyConfig, start time.Time, status int, msg string) {
	_ = db.InsertLog(ctx, db.InsertLogInput{
		Model:            strPtr(cfg.Model),
		ProviderID:       cfg.ProviderID,
		ProviderName:     strPtrOrNil(cfg.ProviderName),
		IsStream:         false,
		StatusCode:       status,
		ProcessingTimeMs: int(time.Since(start).Milliseconds()),
		TargetURL:        strPtr(cfg.TargetURL),
		RequestData:      requestData,
		RequestHeaders:   hdrpkg.ToJSON(cfg.RequestHeaders),
		ErrorMessage:     strPtr(msg),
		Protocol:         strPtr(cfg.Protocol),
		SessionID:        strPtrOrNil(cfg.SessionID),
		UserID:           intPtrOrNil(cfg.UserID),
	})
}
