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
//
// 支持两种 JSON 格式：
// 1. 扁平格式（向后兼容）：{"provider":"openai","model":"gpt-4o",...}
// 2. 嵌套格式（
//
// 当嵌套字段存在时，嵌套字段优先于扁平字段。
type Settings struct {
	// 扁平字段（向后兼容）
	Provider string `json:"provider"`
	Model string `json:"model"`
	MaxTurns int `json:"max_turns"`
	Workspace string `json:"workspace"`
	CompactThreshold int `json:"compact_threshold"`
	APIKey string `json:"api_key,omitempty"`
	Extra map[string]any `json:"extra,omitempty"`

	// 嵌套字段（
	// 注意：JSON tag 使用 "-" 避免与扁平字段 "model" 冲突，
	// 由自定义 UnmarshalJSON 处理 "model" key 的双格式解析。
	ModelCfg *ModelConfig `json:"-"`
	SkillsCfg *SkillsConfig `json:"skills,omitempty"`
	MCPCfg *MCPConfig `json:"mcp,omitempty"`
}

// UnmarshalJSON 实现自定义 JSON 反序列化，支持 "model" 字段的两种格式：
// 1. 字符串格式（扁平）："model": "gpt-4o" → Settings.Model = "gpt-4o"
// 2. 对象格式（嵌套）："model": {"provider":"deepseek","name":"deepseek-chat"} → Settings.ModelCfg = &ModelConfig{...}
func (s *Settings) UnmarshalJSON(data []byte) error {
	// 先解析为通用 map
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	// 处理 "model" 字段：先尝试解析为对象（嵌套格式），失败则解析为字符串（扁平格式）
	if modelRaw, ok := raw["model"]; ok {
		var mc ModelConfig
		if err := json.Unmarshal(modelRaw, &mc); err == nil && (mc.Provider != "" || mc.Name != "") {
			s.ModelCfg = &mc
		} else {
			var modelStr string
			if err := json.Unmarshal(modelRaw, &modelStr); err == nil {
				s.Model = modelStr
			}
		}
		delete(raw, "model") // 避免重复处理
	}

	// 解析 "skills" 字段
	if skillsRaw, ok := raw["skills"]; ok {
		var sc SkillsConfig
		if err := json.Unmarshal(skillsRaw, &sc); err == nil {
			s.SkillsCfg = &sc
		}
		delete(raw, "skills")
	}

	// 解析 "mcp" 字段
	if mcpRaw, ok := raw["mcp"]; ok {
		var mc MCPConfig
		if err := json.Unmarshal(mcpRaw, &mc); err == nil {
			s.MCPCfg = &mc
		}
		delete(raw, "mcp")
	}

	// 解析扁平字段
	if v, ok := raw["provider"]; ok {
		_ = json.Unmarshal(v, &s.Provider)
	}
	if v, ok := raw["max_turns"]; ok {
		_ = json.Unmarshal(v, &s.MaxTurns)
	}
	if v, ok := raw["workspace"]; ok {
		_ = json.Unmarshal(v, &s.Workspace)
	}
	if v, ok := raw["compact_threshold"]; ok {
		_ = json.Unmarshal(v, &s.CompactThreshold)
	}
	if v, ok := raw["api_key"]; ok {
		_ = json.Unmarshal(v, &s.APIKey)
	}
	if v, ok := raw["extra"]; ok {
		_ = json.Unmarshal(v, &s.Extra)
	}

	return nil
}

// ModelConfig
type ModelConfig struct {
	Provider string `json:"provider"`
	Name string `json:"name"`
	BaseURL string `json:"base_url,omitempty"`
	APIKey string `json:"api_key,omitempty"`
	APIKeyEnv string `json:"api_key_env,omitempty"`
	Timeout *int `json:"timeout,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	MaxTokens *int `json:"max_tokens,omitempty"`
}

// SkillsConfig
type SkillsConfig struct {
	Dirs []string `json:"dirs"`
	AutoDiscover *bool `json:"auto_discover,omitempty"`
	Enabled *bool `json:"enabled,omitempty"`
	Disabled []string `json:"disabled,omitempty"`
}

// MCPConfig
type MCPConfig struct {
	ConfigPath string `json:"config_path,omitempty"`
}

// IsAutoDiscoverEnabled 返回是否启用 Skill 自动发现。nil 默认 true。
func (s *SkillsConfig) IsAutoDiscoverEnabled() bool {
	return s == nil || s.AutoDiscover == nil || *s.AutoDiscover
}

// IsSkillsEnabled 返回是否启用 Skill 加载。nil 默认 true。
func (s *SkillsConfig) IsSkillsEnabled() bool {
	return s == nil || s.Enabled == nil || *s.Enabled
}

// GetEffectiveProvider 返回生效的 Provider 名称（嵌套优先）。
func (s *Settings) GetEffectiveProvider() string {
	if s.ModelCfg != nil && s.ModelCfg.Provider != "" {
		return s.ModelCfg.Provider
	}
	return s.Provider
}

// GetEffectiveModel 返回生效的模型名称（嵌套优先）。
func (s *Settings) GetEffectiveModel() string {
	if s.ModelCfg != nil && s.ModelCfg.Name != "" {
		return s.ModelCfg.Name
	}
	return s.Model
}

// GetEffectiveAPIKey 返回生效的 API Key（嵌套优先，支持 api_key_env 环境变量）。
func (s *Settings) GetEffectiveAPIKey() string {
	if s.ModelCfg != nil {
		if s.ModelCfg.APIKey != "" {
			return interpolateEnv(s.ModelCfg.APIKey)
		}
		if s.ModelCfg.APIKeyEnv != "" {
			if v := os.Getenv(s.ModelCfg.APIKeyEnv); v != "" {
				return v
			}
		}
	}
	return s.APIKey
}

// GetEffectiveBaseURL 返回生效的 Base URL（嵌套优先）。
func (s *Settings) GetEffectiveBaseURL() string {
	if s.ModelCfg != nil && s.ModelCfg.BaseURL != "" {
		return interpolateEnv(s.ModelCfg.BaseURL)
	}
	return ""
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
	// 深拷贝嵌套字段
	if m.settings.ModelCfg != nil {
		mc := *m.settings.ModelCfg
		result.ModelCfg = &mc
	}
	if m.settings.SkillsCfg != nil {
		sc := *m.settings.SkillsCfg
		if m.settings.SkillsCfg.Dirs != nil {
			sc.Dirs = make([]string, len(m.settings.SkillsCfg.Dirs))
			copy(sc.Dirs, m.settings.SkillsCfg.Dirs)
		}
		if m.settings.SkillsCfg.Disabled != nil {
			sc.Disabled = make([]string, len(m.settings.SkillsCfg.Disabled))
			copy(sc.Disabled, m.settings.SkillsCfg.Disabled)
		}
		result.SkillsCfg = &sc
	}
	if m.settings.MCPCfg != nil {
		mc := *m.settings.MCPCfg
		result.MCPCfg = &mc
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
	// 对扁平字符串字段执行环境变量插值（$ENV_VAR / ${ENV_VAR}）。
	s.Provider = interpolateEnv(s.Provider)
	s.Model = interpolateEnv(s.Model)
	s.Workspace = interpolateEnv(s.Workspace)
	s.APIKey = interpolateEnv(s.APIKey)
	// 对嵌套 ModelConfig 字段执行环境变量插值
	if s.ModelCfg != nil {
		s.ModelCfg.BaseURL = interpolateEnv(s.ModelCfg.BaseURL)
		s.ModelCfg.APIKey = interpolateEnv(s.ModelCfg.APIKey)
	}
	return &s, nil
}
