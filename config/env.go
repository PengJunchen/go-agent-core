package config

import "os"

// LoadEnv 从环境变量加载配置。
//
// 支持的环境变量：
// - GO_AGENT_PROVIDER
// - GO_AGENT_MODEL
// - GO_AGENT_MAX_TURNS
// - GO_AGENT_WORKSPACE
// - GO_AGENT_COMPACT_THRESHOLD
// - GO_AGENT_API_KEY
func (m *SettingsManager) LoadEnv() {
	envSettings := Settings{}

	if v := os.Getenv("GO_AGENT_PROVIDER"); v != "" {
		envSettings.Provider = v
	}
	if v := os.Getenv("GO_AGENT_MODEL"); v != "" {
		envSettings.Model = v
	}
	if v := os.Getenv("GO_AGENT_MAX_TURNS"); v != "" {
		var n int
		if _, err := parseInt(v, &n); err == nil && n > 0 {
			envSettings.MaxTurns = n
		}
	}
	if v := os.Getenv("GO_AGENT_WORKSPACE"); v != "" {
		envSettings.Workspace = v
	}
	if v := os.Getenv("GO_AGENT_COMPACT_THRESHOLD"); v != "" {
		var n int
		if _, err := parseInt(v, &n); err == nil && n > 0 {
			envSettings.CompactThreshold = n
		}
	}
	if v := os.Getenv("GO_AGENT_API_KEY"); v != "" {
		envSettings.APIKey = v
	}

	m.mu.Lock()
	m.settings = mergeSettings(m.settings, envSettings)
	m.mu.Unlock()
}

// interpolateEnv 将字符串中的 $ENV_VAR 或 ${ENV_VAR} 模式替换为对应的环境变量值。
// 如果环境变量不存在，保留原始文本不变。
func interpolateEnv(s string) string {
	return os.ExpandEnv(s)
}

// parseInt 将字符串解析为 int（安全封装 strconv.Atoi）。
func parseInt(s string, out *int) (int, error) {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errNotANumber
		}
		n = n*10 + int(c-'0')
	}
	*out = n
	return n, nil
}

// errNotANumber 是 parseInt 遇到非数字字符时返回的错误。
var errNotANumber = newParseError("not a valid number")

type parseError struct {
	msg string
}

func (e *parseError) Error() string { return e.msg }

func newParseError(msg string) *parseError { return &parseError{msg: msg} }
