package skill

import (
	"os"
	"path/filepath"
	"strings"
)

// defaultIgnoredDirs 是始终跳过的目录名（硬编码默认值）。
// 对应常见的构建产物和依赖目录。
var defaultIgnoredDirs = map[string]bool{
	"node_modules": true,
	"dist": true,
	".git": true,
	"vendor": true,
}

// parseGitignore 读取 path 处的 .gitignore 文件并返回模式列表。
// 简化实现：每行一个模式，忽略注释（# 开头）和空行。
// 不支持 negation（!pattern）和嵌套目录模式（dir/*/file）的完整语义。
func parseGitignore(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var patterns []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns, nil
}

// matchGitignorePattern 检查 name 是否匹配 patterns 中的任一模式。
// 简化实现：
// - 模式以 / 结尾时，匹配目录名（去除尾部 / 比较）
// - 模式含 * 时，使用简单的 glob 匹配（仅 * 通配符）
// - 其他模式做精确匹配
func matchGitignorePattern(name string, patterns []string) bool {
	for _, pattern := range patterns {
		// 去除尾部 /，用于目录匹配
		cleanPattern := strings.TrimSuffix(pattern, "/")

		if strings.Contains(cleanPattern, "*") {
			if simpleGlobMatch(cleanPattern, name) {
				return true
			}
		} else if name == cleanPattern {
			return true
		}
	}
	return false
}

// simpleGlobMatch 实现仅支持 * 通配符的简单 glob 匹配。
func simpleGlobMatch(pattern, name string) bool {
	// 将 pattern 按 * 分割，依次匹配 name 中的各段。
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == name
	}

	// 第一段必须匹配 name 开头。
	if !strings.HasPrefix(name, parts[0]) {
		return false
	}
	name = name[len(parts[0]):]

	// 中间段必须在 name 中找到。
	for i := 1; i < len(parts)-1; i++ {
		idx := strings.Index(name, parts[i])
		if idx < 0 {
			return false
		}
		name = name[idx+len(parts[i]):]
	}

	// 最后一段必须匹配 name 结尾。
	return strings.HasSuffix(name, parts[len(parts)-1])
}

// isDefaultIgnored 检查目录名是否在硬编码默认忽略列表中。
func isDefaultIgnored(name string) bool {
	return defaultIgnoredDirs[name]
}

// isSymlink 检查 entry 是否为符号链接。
func isSymlink(dir string, entry os.DirEntry) bool {
	if entry.Type()&os.ModeSymlink != 0 {
		return true
	}
	// 某些文件系统需要 Lstat 才能检测符号链接。
	info, err := os.Lstat(filepath.Join(dir, entry.Name()))
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeSymlink != 0
}
