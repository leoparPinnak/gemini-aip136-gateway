package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type OverrideSettings struct {
	OverrideEnabled bool   `json:"override_enabled"` // true = Gateway Zorunlu Enjeksiyon, false = OpenAI Router'ı Baz Al
	TargetModel     string `json:"target_model"`     // "gemini-3.8-flash-high", "gemini-3.8-flash-medium", "gemini-3.8-flash-low"
	ThinkingEffort  string `json:"thinking_effort"`  // "dynamic", "high", "low", "off"
}

type SettingsManager struct {
	settings OverrideSettings
	mu       sync.RWMutex
	filePath string
}

var GlobalSettingsManager *SettingsManager

func InitSettingsManager() {
	execDir, err := os.Getwd()
	if err != nil {
		execDir = "."
	}
	filePath := filepath.Join(execDir, "override_settings.json")

	mgr := &SettingsManager{
		filePath: filePath,
		settings: OverrideSettings{
			OverrideEnabled: false,
			TargetModel:     "gemini-3.8-flash-medium",
			ThinkingEffort:  "dynamic",
		},
	}
	GlobalSettingsManager = mgr

	if data, err := os.ReadFile(filePath); err == nil {
		var s OverrideSettings
		if json.Unmarshal(data, &s) == nil {
			mgr.settings = s
		}
	}
}

func (m *SettingsManager) Get() OverrideSettings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings
}

func (m *SettingsManager) Update(s OverrideSettings) {
	m.mu.Lock()
	m.settings = s

	if data, err := json.MarshalIndent(s, "", "  "); err == nil {
		_ = os.WriteFile(m.filePath, data, 0644)
	}
	m.mu.Unlock()

	BroadcastSettingsChange()
}
