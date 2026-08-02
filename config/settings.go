// Package config 定义 Agent 配置管理。
//
// SettingsManager 从多个来源加载配置并按优先级合并：
// 1. 内置默认值（最低优先级）
// 2. 全局配置（~/.go-agent/settings.json）
// 3. 项目配置（.go-agent/settings.json）
// 4. 环境变量
// 5. CLI 参数（最高优先级，通过 Set 方法设置）
//
// 配置包属于 L0 应用入口层，供 cmd/ 和应用层使用。
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Settings 保存所有 Agent 配置。
type Settings struct {
	Provider string `json:"provider"`
	Model string `json:"model"`
	MaxTurns int `json:"max_turns"`
	Workspace string `json:"workspace"`
	CompactThreshold int `json:"compact_threshold"`
	APIKey string `json:"api_key,omitempty"`
	Extra map[string]any `json:"extra,omitempty"`
}

// SettingsManager 管理多来源配置的加载与合并。
//
// 合并优先级（从低到高）：
// 1. 内置默认值
// 2. 全局配置（~/.go-agent/settings.json）
// 3. 项目配置（.go-agent/settings.json）
// 4. 环境变量
// 5. CLI 参数（通过 Set 方法覆盖）
type SettingsManager struct {
	mu sync.RWMutex
	settings Settings
}

// NewSettingsManager 创建一个使用默认配置的 SettingsManager。
func NewSettingsManager() *SettingsManager {
	return &SettingsManager{
		settings: defaultSettings(),
	}
}

// LoadGlobal 从 ~/.go-agent/settings.json 加载全局配置。
//
// 文件不存在时不报错（视为无全局配置）。
func (m *SettingsManager) LoadGlobal() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil // 无 home 目录则跳过
	}
	path := filepath.Join(home, ".go-agent", "settings.json")
	s, err := loadFromFile(path)
	if err != nil {
		return nil // 文件不存在或读取失败视为无配置
	}
	m.mu.Lock()
	m.settings = mergeSettings(m.settings, *s)
	m.mu.Unlock()
	return nil
}

// LoadProject 从指定项目目录下的 .go-agent/settings.json 加载项目配置。
//
// 文件不存在时不报错（视为无项目配置）。
func (m *SettingsManager) LoadProject(projectDir string) error {
	path := filepath.Join(projectDir, ".go-agent", "settings.json")
	s, err := loadFromFile(path)
	if err != nil {
		return nil // 文件不存在或读取失败视为无配置
	}
	m.mu.Lock()
	m.settings = mergeSettings(m.settings, *s)
	m.mu.Unlock()
	return nil
}

// Merge 将给定 Settings 合并到当前配置（s 中的非零值覆盖当前值）。
func (m *SettingsManager) Merge(s Settings) {
	m.mu.Lock()
	m.settings = mergeSettings(m.settings, s)
	m.mu.Unlock()
}

// Get 返回当前配置的副本。
func (m *SettingsManager) Get() Settings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := m.settings
	if m.settings.Extra != nil {
		result.Extra = make(map[string]any, len(m.settings.Extra))
		for k, v := range m.settings.Extra {
			result.Extra[k] = v
		}
	}
	return result
}

// Set 用给定的 Settings 覆盖当前配置（非零值覆盖）。
func (m *SettingsManager) Set(s Settings) {
	m.mu.Lock()
	m.settings = mergeSettings(m.settings, s)
	m.mu.Unlock()
}

// defaultSettings 返回内置默认配置。
func defaultSettings() Settings {
	return Settings{
		Provider: DefaultProvider,
		Model: DefaultModel,
		MaxTurns: DefaultMaxTurns,
		CompactThreshold: 0,
	}
}

// 默认配置常量。
const (
	DefaultProvider = "openai"
	DefaultModel = "gpt-4o"
	DefaultMaxTurns = 20
)

// loadFromFile 从 JSON 文件加载配置。
func loadFromFile(path string) (*Settings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	// 对字符串字段执行环境变量插值（$ENV_VAR / ${ENV_VAR}）。
	s.Provider = interpolateEnv(s.Provider)
	s.Model = interpolateEnv(s.Model)
	s.Workspace = interpolateEnv(s.Workspace)
	s.APIKey = interpolateEnv(s.APIKey)
	return &s, nil
}
