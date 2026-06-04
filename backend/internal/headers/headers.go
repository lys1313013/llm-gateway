// Package headers centralises HTTP header handling for the gateway —
// specifically the list of headers that must be stripped before being
// persisted in api_logs.
package headers

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Sensitive lists header names (lowercased) that carry credentials and
// must never be persisted in api_logs. Lookup is case-insensitive.
var Sensitive = map[string]struct{}{
	"authorization":       {},
	"x-api-key":           {},
	"api-key":             {},
	"cookie":              {},
	"set-cookie":          {},
	"proxy-authorization": {},
}

// FromMap returns a copy of h with sensitive entries removed. The input
// is left unchanged.
func FromMap(h map[string]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if _, drop := Sensitive[strings.ToLower(k)]; drop {
			continue
		}
		out[k] = v
	}
	return out
}

// FromHTTPHeader flattens an http.Header to a single-value map (joining
// multi-value entries with ", ") and drops sensitive entries.
func FromHTTPHeader(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if len(v) == 0 {
			continue
		}
		if _, drop := Sensitive[strings.ToLower(k)]; drop {
			continue
		}
		out[k] = strings.Join(v, ", ")
	}
	return out
}

// ToJSON marshals a header map to JSON bytes, returning nil for an empty
// map so the corresponding JSONB column is stored as SQL NULL.
func ToJSON(h map[string]string) []byte {
	if len(h) == 0 {
		return nil
	}
	b, err := json.Marshal(h)
	if err != nil {
		return nil
	}
	return b
}
