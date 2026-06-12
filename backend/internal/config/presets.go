package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// Preset is a vendor template that pre-fills the provider form. Only the
// structural fields are populated; api_key is always user-supplied.
type Preset struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Description      string `json:"description,omitempty"`
	OpenAIBaseURL    string `json:"openai_base_url,omitempty"`
	AnthropicBaseURL string `json:"anthropic_base_url,omitempty"`
	QuotaURL         string `json:"quota_url,omitempty"`
	QuotaFormat      string `json:"quota_format,omitempty"`
	Remark           string `json:"remark,omitempty"`
}

// PresetsEnvVar, when set to a readable file path, overrides the default
// lookup so operators can ship their own catalog without code changes.
const PresetsEnvVar = "PROVIDER_PRESETS_FILE"

// DefaultPresetsRelPath is the path searched when no env override is set.
// Resolved relative to the process working directory.
const DefaultPresetsRelPath = "config/provider_presets.json"

// LoadPresets returns the active provider preset catalog. Order is preserved
// from the source file. An empty slice is returned if the file is missing or
// malformed — the UI treats that as "no presets available".
func LoadPresets() ([]Preset, error) {
	path, err := presetsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read presets %s: %w", path, err)
	}
	var ps []Preset
	if err := json.Unmarshal(data, &ps); err != nil {
		return nil, fmt.Errorf("parse presets %s: %w", path, err)
	}
	slog.Info("presets loaded", "path", path, "count", len(ps))
	return ps, nil
}

// presetsPath resolves which file to load. The env override always wins;
// otherwise we look for DefaultPresetsRelPath relative to the working dir.
func presetsPath() (string, error) {
	if p := os.Getenv(PresetsEnvVar); p != "" {
		return p, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	return filepath.Join(cwd, DefaultPresetsRelPath), nil
}
