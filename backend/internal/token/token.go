// Package token provides token estimation, mirroring token_utils.py
// in the Python backend.
//
// Uses github.com/pkoukk/tiktoken-go for BPE-compatible encoding.
package token

import (
	"encoding/json"
	"log/slog"
	"sync"

	tiktoken "github.com/pkoukk/tiktoken-go"

	"github.com/lys1313013/llm-gateway/backend/internal/models"
)

var (
	cacheMu sync.RWMutex
	cache   = map[string]*tiktoken.Tiktoken{}
)

func getEncoder(model string) *tiktoken.Tiktoken {
	cacheMu.RLock()
	if enc, ok := cache[model]; ok {
		cacheMu.RUnlock()
		return enc
	}
	cacheMu.RUnlock()

	enc, err := tiktoken.EncodingForModel(model)
	if err != nil {
		enc, err = tiktoken.GetEncoding("cl100k_base")
		if err != nil {
			slog.Error("tiktoken GetEncoding cl100k_base failed", "err", err)
			return nil
		}
	}
	cacheMu.Lock()
	cache[model] = enc
	cacheMu.Unlock()
	return enc
}

// EstimateTokens returns the BPE token count for text under the given model.
// Falls back to cl100k_base if the model is unknown, and to a length-based
// heuristic if even that fails.
func EstimateTokens(text, model string) int {
	if text == "" {
		return 0
	}
	enc := getEncoder(model)
	if enc == nil {
		return max1(len(text)/3)
	}
	return len(enc.Encode(text, nil, nil))
}

// EstimateAnthropicTokens uses cl100k_base as an approximation (the Python
// backend does the same).
func EstimateAnthropicTokens(text string) int {
	return EstimateTokens(text, "cl100k_base")
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

// ---------------------------------------------------------------------------
// Normalize usage — convert upstream usage payloads into the canonical
// {prompt, completion, total, cache_*, raw} shape used in api_logs.
// ---------------------------------------------------------------------------

// NormalizeUsage returns nil if the payload is empty or unrecognized.
func NormalizeUsage(raw json.RawMessage) *models.NormalizedUsage {
	if len(raw) == 0 {
		return nil
	}
	var u map[string]any
	if err := json.Unmarshal(raw, &u); err != nil {
		return nil
	}
	out := &models.NormalizedUsage{Raw: raw}
	if v, ok := u["input_tokens"]; ok {
		// Anthropic
		out.PromptTokens = asInt(v) + asInt(u["cache_creation_input_tokens"]) + asInt(u["cache_read_input_tokens"])
		out.CompletionTokens = asInt(u["output_tokens"])
		out.TotalTokens = out.PromptTokens + out.CompletionTokens
		out.CacheCreationInputTokens = asInt(u["cache_creation_input_tokens"])
		out.CacheReadInputTokens = asInt(u["cache_read_input_tokens"])
		return out
	}
	if v, ok := u["prompt_tokens"]; ok {
		// OpenAI
		out.PromptTokens = asInt(v)
		out.CompletionTokens = asInt(u["completion_tokens"])
		if t, ok := u["total_tokens"]; ok {
			out.TotalTokens = asInt(t)
		} else {
			out.TotalTokens = out.PromptTokens + out.CompletionTokens
		}
		if pd, ok := u["prompt_tokens_details"].(map[string]any); ok {
			out.CacheReadInputTokens = asInt(pd["cached_tokens"])
		}
		return out
	}
	return nil
}

func asInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	case json.Number:
		n, _ := t.Int64()
		return int(n)
	}
	return 0
}

// ---------------------------------------------------------------------------
// Estimate from request/response when the upstream omits usage
// ---------------------------------------------------------------------------

// CalculateOpenAIUsage estimates prompt + completion tokens for an OpenAI
// chat request/response when the upstream didn't return a usage block.
func CalculateOpenAIUsage(requestData, responseData any) models.NormalizedUsage {
	model := "gpt-3.5-turbo"
	if m, ok := requestData.(map[string]any)["model"].(string); ok && m != "" {
		model = m
	}

	prompt := flattenOpenAIPrompt(requestData)
	promptTok := EstimateTokens(prompt, model)

	completion := flattenOpenAICompletion(responseData)
	completionTok := EstimateTokens(completion, model)

	return models.NormalizedUsage{
		PromptTokens:     promptTok,
		CompletionTokens: completionTok,
		TotalTokens:      promptTok + completionTok,
	}
}

func flattenOpenAIPrompt(req any) string {
	var b []byte
	if m, ok := req.(map[string]any); ok {
		if msgs, ok := m["messages"].([]any); ok {
			for _, raw := range msgs {
				msg, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				if s, ok := msg["content"].(string); ok {
					b = append(b, s...)
				} else if arr, ok := msg["content"].([]any); ok {
					for _, blk := range arr {
						if bm, ok := blk.(map[string]any); ok {
							if bm["type"] == "text" {
								if s, ok := bm["text"].(string); ok {
									b = append(b, s...)
								}
							}
						}
					}
				}
				if rc, ok := msg["reasoning_content"].(string); ok && rc != "" {
					b = append(b, rc...)
				}
			}
		}
		if tools, ok := m["tools"]; ok {
			tb, _ := json.Marshal(tools)
			b = append(b, tb...)
		}
	}
	return string(b)
}

func flattenOpenAICompletion(resp any) string {
	var b []byte
	m, ok := resp.(map[string]any)
	if !ok {
		return ""
	}
	// flat-aggregated streaming response
	if s, ok := m["content"].(string); ok {
		b = append(b, s...)
	}
	if s, ok := m["reasoning_content"].(string); ok {
		b = append(b, s...)
	}
	if tc, ok := m["tool_calls"]; ok && tc != nil {
		tb, _ := json.Marshal(tc)
		b = append(b, tb...)
	}
	if fc, ok := m["function_call"]; ok && fc != nil {
		fb, _ := json.Marshal(fc)
		b = append(b, fb...)
	}
	// standard non-streaming response
	if choices, ok := m["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if msg, ok := choice["message"].(map[string]any); ok {
				if s, ok := msg["content"].(string); ok {
					b = append(b, s...)
				}
				if s, ok := msg["reasoning_content"].(string); ok {
					b = append(b, s...)
				}
				if tc, ok := msg["tool_calls"]; ok && tc != nil {
					tb, _ := json.Marshal(tc)
					b = append(b, tb...)
				}
				if fc, ok := msg["function_call"]; ok && fc != nil {
					fb, _ := json.Marshal(fc)
					b = append(b, fb...)
				}
			} else if text, ok := choice["text"].(string); ok {
				b = append(b, text...)
			}
		}
	}
	return string(b)
}

// CalculateAnthropicUsage estimates input/output tokens for an Anthropic
// Messages request/response when the upstream didn't return a usage block.
func CalculateAnthropicUsage(requestData, responseData any) models.NormalizedUsage {
	var b []byte
	if m, ok := requestData.(map[string]any); ok {
		// system can be a string or array of content blocks
		switch s := m["system"].(type) {
		case string:
			b = append(b, s...)
		case []any:
			for _, blk := range s {
				if bm, ok := blk.(map[string]any); ok {
					if t, ok := bm["text"].(string); ok {
						b = append(b, t...)
					}
				}
			}
		}
		if msgs, ok := m["messages"].([]any); ok {
			for _, raw := range msgs {
				msg, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				switch c := msg["content"].(type) {
				case string:
					b = append(b, c...)
				case []any:
					for _, blk := range c {
						if bm, ok := blk.(map[string]any); ok {
							if bm["type"] == "text" {
								if t, ok := bm["text"].(string); ok {
									b = append(b, t...)
								}
							}
						}
					}
				}
			}
		}
		if tools, ok := m["tools"]; ok {
			tb, _ := json.Marshal(tools)
			b = append(b, tb...)
		}
	}
	in := EstimateAnthropicTokens(string(b))

	var out []byte
	if m, ok := responseData.(map[string]any); ok {
		if blocks, ok := m["content"].([]any); ok {
			for _, raw := range blocks {
				blk, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				switch blk["type"] {
				case "text":
					if s, ok := blk["text"].(string); ok {
						out = append(out, s...)
					}
				case "tool_use":
					tb, _ := json.Marshal(blk["input"])
					out = append(out, tb...)
				case "thinking":
					if s, ok := blk["thinking"].(string); ok {
						out = append(out, s...)
					}
				}
			}
		}
	}
	outTok := EstimateAnthropicTokens(string(out))

	return models.NormalizedUsage{
		PromptTokens:     in,
		CompletionTokens: outTok,
		TotalTokens:      in + outTok,
	}
}
