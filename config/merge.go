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
	return dst
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
