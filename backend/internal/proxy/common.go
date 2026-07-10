package proxy

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// timeoutFromConfig converts the user-facing timeout config (seconds; -1 =
// no timeout) into a net/http timeout value.
func timeoutFromConfig(seconds int) time.Duration {
	if seconds == -1 {
		return 0 // no timeout
	}
	if seconds <= 0 {
		return 60 * time.Second
	}
	return time.Duration(seconds) * time.Second
}

// streamingTransport disables connection reuse for long-lived streams
// (mostly a no-op — http.Client handles this fine), but we use it as a
// place to plug in tracing / metrics later.
var streamingTransport = &http.Transport{
	DisableCompression: true,
	MaxIdleConns:       100,
	IdleConnTimeout:    90 * time.Second,
}

func strPtr(s string) *string    { return &s }
func intPtr(i int) *int          { return &i }

// intPtrOrNil returns nil for zero values so the DB stores NULL rather than 0.
func intPtrOrNil(i int) *int {
	if i <= 0 {
		return nil
	}
	return &i
}

// strPtrOrNil returns nil for empty strings so the DB stores NULL rather
// than an empty string for "no provider" cases.
func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// lastMessagePreviewLen is the max number of runes captured from the last
// user/assistant message for the api_logs.last_message_preview column.
const lastMessagePreviewLen = 100

// extractLastMessagePreview pulls a short text preview from the last
// conversational message in an LLM request body. Returns nil when no text
// can be extracted (non-JSON body, no messages array, last message has no
// textual content). The result is capped at lastMessagePreviewLen runes.
//
// Supports the shapes used across OpenAI, Anthropic and Responses APIs:
//   - messages[] with string or content-block array content
//   - input[] (Responses API) with string or content-block array content
//   - input as a plain string
func extractLastMessagePreview(requestData []byte) *string {
	if len(requestData) == 0 {
		return nil
	}
	var body map[string]any
	if err := json.Unmarshal(requestData, &body); err != nil {
		return nil
	}

	// Candidates: "messages" (chat completions, anthropic) or "input" (responses)
	var lastMsg any
	if msgs, ok := body["messages"].([]any); ok && len(msgs) > 0 {
		lastMsg = msgs[len(msgs)-1]
	} else if inputs, ok := body["input"].([]any); ok && len(inputs) > 0 {
		lastMsg = inputs[len(inputs)-1]
	} else if s, ok := body["input"].(string); ok && s != "" {
		return strPtrOrNil(truncateRunes(s, lastMessagePreviewLen))
	}
	if lastMsg == nil {
		return nil
	}

	text := extractTextFromMessage(lastMsg)
	return strPtrOrNil(truncateRunes(text, lastMessagePreviewLen))
}

// extractTextFromMessage pulls the first textual representation out of a
// single message object. Returns "" for tool-role messages without content.
func extractTextFromMessage(msg any) string {
	m, ok := msg.(map[string]any)
	if !ok {
		return ""
	}
	content, ok := m["content"]
	if !ok {
		return ""
	}
	switch c := content.(type) {
	case string:
		return c
	case []any:
		// Multi-part content (OpenAI multimodal / Anthropic content blocks)
		var parts []string
		for _, part := range c {
			p, ok := part.(map[string]any)
			if !ok {
				continue
			}
			// Anthropic / OpenAI text blocks use "text"
			if t, ok := p["text"].(string); ok && t != "" {
				parts = append(parts, t)
				continue
			}
			// Some payloads use "content" directly
			if t, ok := p["content"].(string); ok && t != "" {
				parts = append(parts, t)
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

// truncateRunes returns s trimmed to at most maxRunes runes. When the
// string is longer it is trimmed and "…" is appended, so callers always
// get an indication that truncation happened.
func truncateRunes(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	// Collapse runs of whitespace into single spaces so a JSON-formatted
	// multi-line message reads cleanly in the DB preview column.
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes-1]) + "…"
}
