package proxy

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// parseAnthropicEvents accepts SSE fields with or without the optional space
// after the colon. Event names are retained even for providers whose JSON does
// not include Anthropic's type field (for example DashScope's error events).
func parseAnthropicEvents(raw []byte) ([]map[string]any, error) {
	var events []map[string]any
	var parseErr error
	var event string
	var data []string
	dispatch := func() {
		defer func() { event, data = "", nil }()
		if len(data) == 0 {
			return
		}
		payload := strings.Join(data, "\n")
		if payload == "[DONE]" {
			return
		}
		var value map[string]any
		if err := json.Unmarshal([]byte(payload), &value); err != nil || value == nil {
			if parseErr == nil {
				parseErr = fmt.Errorf("invalid JSON in SSE event %q", event)
			}
			return
		}
		if event == "error" || value["type"] == nil {
			value["type"] = event
		}
		events = append(events, value)
	}
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			dispatch()
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event = value
		case "data":
			data = append(data, value)
		}
	}
	dispatch()
	if err := scanner.Err(); err != nil {
		parseErr = errors.Join(parseErr, fmt.Errorf("read SSE: %w", err))
	}
	return events, parseErr
}

// normalizeAnthropicError preserves provider diagnostics while giving clients
// a standard Anthropic error envelope. Do not silently remove request options
// when a provider rejects them.
func normalizeAnthropicError(payload map[string]any, status int) (map[string]any, string) {
	typ := "api_error"
	switch status {
	case 400, 422:
		typ = "invalid_request_error"
	case 401:
		typ = "authentication_error"
	case 403:
		typ = "permission_error"
	case 404:
		typ = "not_found_error"
	case 429:
		typ = "rate_limit_error"
	}
	detail, _ := payload["error"].(map[string]any)
	if detail == nil {
		detail = payload
	}
	message, _ := detail["message"].(string)
	if message == "" {
		message, _ = payload["error"].(string)
	}
	if message == "" {
		message = fmt.Sprintf("Upstream returned status %d", status)
	}
	errValue := map[string]any{"type": typ, "message": message}
	if detail != nil {
		// Preserve the original nested Anthropic error fields.
		if _, ok := payload["error"].(map[string]any); ok {
			for k, v := range detail {
				errValue[k] = v
			}
		}
		if code, ok := detail["code"]; ok {
			errValue["code"] = code
			message = fmt.Sprintf("%v: %s", code, message)
		}
	}
	envelope := map[string]any{"type": "error", "error": errValue}
	if id, ok := payload["request_id"]; ok {
		envelope["request_id"] = id
		message += fmt.Sprintf(" (request_id: %v)", id)
	}
	return envelope, message
}

func anthropicHTTPError(body []byte, status int) (logData, clientData []byte, message string) {
	var payload map[string]any
	if json.Unmarshal(body, &payload) == nil && payload != nil {
		logData = body
	} else {
		logData, _ = json.Marshal(map[string]any{"raw": string(body)})
		events, _ := parseAnthropicEvents(body)
		for _, ev := range events {
			if ev["type"] == "error" {
				payload = ev
				break
			}
		}
	}
	envelope, message := normalizeAnthropicError(payload, status)
	clientData, _ = json.Marshal(envelope)
	return logData, clientData, message
}
