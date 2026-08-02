package config

// mergeSettings 将 src 合并到 dst（src 中的非零值覆盖 dst）。
// Extra map[string]any 字段执行深度递归合并：嵌套 map 逐键合并，
// 非map值由 src 覆盖 dst。
func mergeSettings(dst, src Settings) Settings {
	if src.Provider != "" {
		dst.Provider = src.Provider
	}
	if src.Model != "" {
		dst.Model = src.Model
	}
	if src.MaxTurns != 0 {
		dst.MaxTurns = src.MaxTurns
	}
	if src.Workspace != "" {
		dst.Workspace = src.Workspace
	}
	if src.CompactThreshold != 0 {
		dst.CompactThreshold = src.CompactThreshold
	}
	if src.APIKey != "" {
		dst.APIKey = src.APIKey
	}
	if src.Extra != nil {
		if dst.Extra == nil {
			dst.Extra = make(map[string]any)
		}
		dst.Extra = deepMergeMaps(dst.Extra, src.Extra)
	}
	// 嵌套字段合并
	if src.ModelCfg != nil {
		if dst.ModelCfg == nil {
			dst.ModelCfg = &ModelConfig{}
		}
		dst.ModelCfg = mergeModelConfig(dst.ModelCfg, src.ModelCfg)
	}
	if src.SkillsCfg != nil {
		if dst.SkillsCfg == nil {
			dst.SkillsCfg = &SkillsConfig{}
		}
		dst.SkillsCfg = mergeSkillsConfig(dst.SkillsCfg, src.SkillsCfg)
	}
	if src.MCPCfg != nil {
		if dst.MCPCfg == nil {
			dst.MCPCfg = &MCPConfig{}
		}
		dst.MCPCfg = mergeMCPConfig(dst.MCPCfg, src.MCPCfg)
	}
	return dst
}

// mergeModelConfig 合并 ModelConfig（src 非零值覆盖 dst）。
func mergeModelConfig(dst, src *ModelConfig) *ModelConfig {
	result := *dst
	if src.Provider != "" {
		result.Provider = src.Provider
	}
	if src.Name != "" {
		result.Name = src.Name
	}
	if src.BaseURL != "" {
		result.BaseURL = src.BaseURL
	}
	if src.APIKey != "" {
		result.APIKey = src.APIKey
	}
	if src.APIKeyEnv != "" {
		result.APIKeyEnv = src.APIKeyEnv
	}
	if src.Timeout != nil {
		result.Timeout = src.Timeout
	}
	if src.Temperature != nil {
		result.Temperature = src.Temperature
	}
	if src.MaxTokens != nil {
		result.MaxTokens = src.MaxTokens
	}
	return &result
}

// mergeSkillsConfig 合并 SkillsConfig（src 非零值覆盖 dst）。
func mergeSkillsConfig(dst, src *SkillsConfig) *SkillsConfig {
	result := *dst
	if len(src.Dirs) > 0 {
		result.Dirs = src.Dirs
	}
	if src.AutoDiscover != nil {
		result.AutoDiscover = src.AutoDiscover
	}
	if src.Enabled != nil {
		result.Enabled = src.Enabled
	}
	if len(src.Disabled) > 0 {
		result.Disabled = src.Disabled
	}
	return &result
}

// mergeMCPConfig 合并 MCPConfig（src 非零值覆盖 dst）。
func mergeMCPConfig(dst, src *MCPConfig) *MCPConfig {
	result := *dst
	if src.ConfigPath != "" {
		result.ConfigPath = src.ConfigPath
	}
	return &result
}

// deepMergeMaps 递归合并 src 到 dst。
// 对于同一 key：
// - 若 dst 和 src 的值都是 map[string]any，递归合并
// - 否则 src 的值覆盖 dst 的值
func deepMergeMaps(dst, src map[string]any) map[string]any {
	for k, srcVal := range src {
		dstVal, dstExists := dst[k]
		srcMap, srcIsMap := srcVal.(map[string]any)
		dstMap, dstIsMap := dstVal.(map[string]any)

		if dstExists && srcIsMap && dstIsMap {
			dst[k] = deepMergeMaps(dstMap, srcMap)
		} else {
			dst[k] = srcVal
		}
	}
	return dst
}
