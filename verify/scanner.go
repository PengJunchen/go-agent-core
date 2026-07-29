// Package verify 提供代码校验框架（AST 扫描 + 日志验证 + 泄漏检测）。
//
// 沿用校验范式，新增 go-agent-core 专属规则：
// - IFACE-001: llm/provider, memory/*, capability/* 不得 import llm/adapter/
// - IFACE-002: 接口层不得 import cloudwego/eino
// - IFACE-003: agent/ 不得 import llm/adapter/
// - LOG-001: LoopAgent 构造必须有非 nil ExecLogger
// - SCAN-010: Provider 路由硬编码检测
// - SCAN-011: 工具事件泄露检测
// - SCAN-012: 日志绕过检测
// - SCAN-013: 接口实现完整性检测
package verify

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// Severity 枚举扫描结果严重度。
type Severity string

const (
	SeverityError Severity = "Error"
	SeverityWarning Severity = "Warning"
)

// Violation 是一条扫描违规。
type Violation struct {
	RuleID string
	Severity Severity
	File string
	Line int
	Message string
}

// Scanner 是 AST 扫描器。
type Scanner struct {
	fset *token.FileSet
	rules []ScanRule
}

// ScanRule 是一条扫描规则。
type ScanRule struct {
	ID string
	Severity Severity
	Check func(file *ast.File, path string) []Violation
}

// NewScanner 创建扫描器，预装所有规则。
func NewScanner() *Scanner {
	s := &Scanner{fset: token.NewFileSet()}
	s.rules = []ScanRule{
		s.ruleIFACE001(),
		s.ruleIFACE002(),
		s.ruleIFACE003(),
		s.ruleSCAN010(),
	}
	return s
}

// ScanDir 扫描指定目录下的所有 .go 文件。
func (s *Scanner) ScanDir(root string) ([]Violation, error) {
	var violations []Violation
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// 跳过 vendor
		if strings.Contains(path, "vendor/") {
			return nil
		}
		file, err := parser.ParseFile(s.fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil // 跳过解析失败的文件
		}
		for _, rule := range s.rules {
			violations = append(violations, rule.Check(file, path)...)
		}
		return nil
	})
	return violations, err
}

// ─── IFACE-001: 接口层不得 import adapter ────────────────────────

func (s *Scanner) ruleIFACE001() ScanRule {
	ifacePkgs := []string{"llm/provider", "llm/stream", "llm/message", "llm/transform",
		"memory/session", "memory/context", "memory/compactor", "memory/log",
		"capability/registry", "capability/toolhook", "capability/skill", "capability/mcp"}
	return ScanRule{
		ID: "IFACE-001",
		Severity: SeverityError,
		Check: func(file *ast.File, path string) []Violation {
			if !containsAny(path, ifacePkgs) {
				return nil
			}
			return checkForbiddenImport(file, path, "IFACE-001",
				[]string{"llm/adapter/", "github.com/cloudwego/eino"}, ifacePkgs, s.fset)
		},
	}
}

// ─── IFACE-002: 接口层不得 import eino ───────────────────────────

func (s *Scanner) ruleIFACE002() ScanRule {
	return ScanRule{
		ID: "IFACE-002",
		Severity: SeverityError,
		Check: func(file *ast.File, path string) []Violation {
			if !strings.Contains(path, "llm/provider/") &&
				!strings.Contains(path, "llm/stream/") &&
				!strings.Contains(path, "llm/message/") {
				return nil
			}
			for _, imp := range file.Imports {
				impPath := strings.Trim(imp.Path.Value, `"`)
				if strings.Contains(impPath, "cloudwego/eino") {
					return []Violation{{
						RuleID: "IFACE-002",
						Severity: SeverityError,
						File: path,
						Line: s.fset.Position(imp.Pos()).Line,
						Message: fmt.Sprintf("interface layer must not import eino: %s", impPath),
					}}
				}
			}
			return nil
		},
	}
}

// ─── IFACE-003: agent/ 不得 import adapter ───────────────────────

func (s *Scanner) ruleIFACE003() ScanRule {
	return ScanRule{
		ID: "IFACE-003",
		Severity: SeverityError,
		Check: func(file *ast.File, path string) []Violation {
			if !strings.Contains(path, "agent/") {
				return nil
			}
			for _, imp := range file.Imports {
				impPath := strings.Trim(imp.Path.Value, `"`)
				if strings.Contains(impPath, "llm/adapter/") {
					return []Violation{{
						RuleID: "IFACE-003",
						Severity: SeverityError,
						File: path,
						Line: s.fset.Position(imp.Pos()).Line,
						Message: fmt.Sprintf("agent layer must not import adapter: %s", impPath),
					}}
				}
			}
			return nil
		},
	}
}

// ─── SCAN-010: Provider 路由硬编码 ───────────────────────────────

func (s *Scanner) ruleSCAN010() ScanRule {
	return ScanRule{
		ID: "SCAN-010",
		Severity: SeverityError,
		Check: func(file *ast.File, path string) []Violation {
			// 检测 switch/if-else 硬编码 provider 名（如 switch "openai"）
			var violations []Violation
			ast.Inspect(file, func(n ast.Node) bool {
				basic, ok := n.(*ast.BasicLit)
				if !ok || basic.Kind != token.STRING {
					return true
				}
				val := strings.Trim(basic.Value, `"`)
				if val == "openai" || val == "anthropic" || val == "gemini" {
					// 排除 adapter 层（允许在 adapter 中使用）
					if strings.Contains(path, "adapter/") {
						return true
					}
					// 排除 verify/ 层（规则定义中列举 provider 名）
					if strings.Contains(path, "verify/") {
						return true
					}
					// 排除测试文件
					if strings.HasSuffix(path, "_test.go") {
						return true
					}
					violations = append(violations, Violation{
						RuleID: "SCAN-010",
						Severity: SeverityError,
						File: path,
						Line: s.fset.Position(basic.Pos()).Line,
						Message: fmt.Sprintf("hardcoded provider name %q — use ProviderRegistry instead", val),
					})
				}
				return true
			})
			return violations
		},
	}
}

// ─── 辅助函数 ────────────────────────────────────────────────────

func checkForbiddenImport(file *ast.File, path, ruleID string, forbidden []string, ifacePkgs []string, fset *token.FileSet) []Violation {
	var violations []Violation
	for _, imp := range file.Imports {
		impPath := strings.Trim(imp.Path.Value, `"`)
		for _, f := range forbidden {
			if strings.Contains(impPath, f) {
				violations = append(violations, Violation{
					RuleID: ruleID,
					Severity: SeverityError,
					File: path,
					Line: fset.Position(imp.Pos()).Line,
					Message: fmt.Sprintf("interface layer must not import %s: %s", f, impPath),
				})
			}
		}
	}
	return violations
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// FormatViolations 格式化违规列表为可读字符串。
func FormatViolations(violations []Violation) string {
	if len(violations) == 0 {
		return "✅ No violations found"
	}
	var sb strings.Builder
	for _, v := range violations {
		sb.WriteString(fmt.Sprintf("❌ %s [%s] %s:%d — %s\n",
			v.RuleID, v.Severity, v.File, v.Line, v.Message))
	}
	return sb.String()
}
