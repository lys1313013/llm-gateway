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

	"github.com/lys1313013/llm-gateway/backend/internal/db"
	hdrpkg "github.com/lys1313013/llm-gateway/backend/internal/headers"
	"github.com/lys1313013/llm-gateway/backend/internal/models"
	"github.com/lys1313013/llm-gateway/backend/internal/token"
)

// ---------------------------------------------------------------------------
// OpenAI Responses API  (/v1/responses)
// ---------------------------------------------------------------------------

// HandleResponses processes a Responses-API request and returns the same
// (statusCode, headers, body, isStreaming, error) tuple as HandleOpenAI.
// Streaming bodies use the Responses event format (named SSE events such as
// `response.output_text.delta`), which the caller must forward verbatim.
func HandleResponses(ctx context.Context, requestData []byte, cfg models.ProxyConfig) (int, http.Header, io.ReadCloser, bool, error) {
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
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	if cfg.LogRequests {
		slog.Info("responses proxy: forwarding", "url", cfg.TargetURL, "model", cfg.Model)
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
		_ = db.InsertLog(ctx, db.InsertLogInput{
			Model:           strPtr(modelFromRequest(probe, cfg.Model)),
			ProviderID:      cfg.ProviderID,
			ProviderName:    strPtrOrNil(cfg.ProviderName),
			IsStream:        isStream,
			StatusCode:      resp.StatusCode,
			ProcessingTimeMs: int(time.Since(start).Milliseconds()),
			TargetURL:       strPtr(cfg.TargetURL),
			RequestData:     requestData,
			ResponseData:    body,
			RequestHeaders:  hdrpkg.ToJSON(cfg.RequestHeaders),
			ResponseHeaders: hdrpkg.ToJSON(hdrpkg.FromHTTPHeader(resp.Header)),
			ErrorMessage:    strPtr(fmt.Sprintf("Target API returned error: %d", resp.StatusCode)),
			Protocol:        strPtr(cfg.Protocol),
			SessionID:       strPtrOrNil(cfg.SessionID),
			UserID:          intPtrOrNil(cfg.UserID),
		})
		return resp.StatusCode, nil, io.NopCloser(strings.NewReader(string(body))), false, nil
	}

	if isStream {
		streamer := &responsesStreamer{
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

// responsesStreamer wraps the upstream SSE body for Responses API streams.
// It captures the raw bytes for logging and, on EOF, aggregates the stream
// into a log row.
type responsesStreamer struct {
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

func (s *responsesStreamer) Read(p []byte) (int, error) {
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

func (s *responsesStreamer) Close() error {
	if !s.finished {
		s.finished = true
		s.finalize()
	}
	return s.resp.Close()
}

func (s *responsesStreamer) finalize() {
	if !s.logResponses {
		return
	}
	events := s.collectEvents()
	agg := aggregateResponsesStream(events)
	aggJSON, _ := json.Marshal(agg)

	usageBytes, _ := json.Marshal(agg["usage"])
	norm := token.NormalizeUsage(usageBytes)
	if norm == nil {
		var req any
		_ = json.Unmarshal(s.requestData, &req)
		n := token.CalculateOpenAIUsage(req, agg)
		norm = &n
	}

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

// responsesEvent represents a single SSE event parsed from the Responses
// stream: an event name (e.g. "response.output_text.delta") plus the JSON
// payload.
type responsesEvent struct {
	event   string
	payload map[string]any
}

func (s *responsesStreamer) collectEvents() []responsesEvent {
	var out []responsesEvent
	scanner := bufio.NewScanner(strings.NewReader(string(s.pending)))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var currentEvent string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			// blank line = event boundary
			currentEvent = ""
			continue
		}
		if strings.HasPrefix(line, "event:") {
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(payload), &m); err == nil {
			// Fall back to type field if event name not set
			ev := currentEvent
			if ev == "" {
				if t, ok := m["type"].(string); ok {
					ev = t
				}
			}
			out = append(out, responsesEvent{event: ev, payload: m})
		}
	}
	return out
}

// aggregateResponsesStream turns a slice of Responses SSE events into the
// same shape we write to api_logs.response_data for non-streaming calls:
//
//	{
//	  "role": "assistant",
//	  "content": "...",
//	  "tool_calls": [...],
//	  "usage": {...}
//	}
func aggregateResponsesStream(events []responsesEvent) map[string]any {
	agg := map[string]any{
		"role":    "assistant",
		"content": "",
	}
	// tool/function call aggregation, keyed by sequential order
	type toolAcc struct {
		id        string
		name      string
		arguments string
		callID    string
	}
	var toolCalls []toolAcc
	toolIdx := -1
	var usage map[string]any

	for _, ev := range events {
		switch ev.event {
		case "response.output_text.delta":
			if delta, ok := ev.payload["delta"].(string); ok {
				existing, _ := agg["content"].(string)
				agg["content"] = existing + delta
			}
		case "response.output_item.added":
			// A new output item begins. If it's a function_call we track its
			// index for subsequent argument deltas.
			if item, ok := ev.payload["item"].(map[string]any); ok {
				if t, _ := item["type"].(string); t == "function_call" {
					toolIdx++
					acc := toolAcc{}
					if id, ok := item["id"].(string); ok {
						acc.callID = id
					}
					if n, ok := item["name"].(string); ok {
						acc.name = n
					}
					if a, ok := item["arguments"].(string); ok {
						acc.arguments = a
					}
					toolCalls = append(toolCalls, acc)
				}
			}
		case "response.function_call_arguments.delta":
			if toolIdx >= 0 && toolIdx < len(toolCalls) {
				if delta, ok := ev.payload["delta"].(string); ok {
					toolCalls[toolIdx].arguments += delta
				}
			}
		case "response.function_call_arguments.done":
			if toolIdx >= 0 && toolIdx < len(toolCalls) {
				if args, ok := ev.payload["arguments"].(string); ok {
					toolCalls[toolIdx].arguments = args
				}
				if n, ok := ev.payload["name"].(string); ok {
					toolCalls[toolIdx].name = n
				}
				if id, ok := ev.payload["call_id"].(string); ok {
					toolCalls[toolIdx].callID = id
				}
			}
		case "response.output_text.done":
			// text is already aggregated via deltas
		case "response.completed":
			if r, ok := ev.payload["response"].(map[string]any); ok {
				if u, ok := r["usage"].(map[string]any); ok {
					usage = u
				}
			}
		}
	}

	if len(toolCalls) > 0 {
		raw := make([]any, 0, len(toolCalls))
		for _, tc := range toolCalls {
			raw = append(raw, map[string]any{
				"id":   tc.callID,
				"type": "function",
				"function": map[string]any{
					"name":      tc.name,
					"arguments": tc.arguments,
				},
			})
		}
		agg["tool_calls"] = raw
	}
	if usage != nil {
		agg["usage"] = usage
	}
	return agg
}
