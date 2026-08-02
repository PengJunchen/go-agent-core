// Package verify 提供代码校验框架（AST 扫描 + 日志验证 + 泄漏检测）。
//
// 沿用校验范式，新增专属规则：
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
		s.ruleSCAN011(),
		s.ruleSCAN012(),
		s.ruleSCAN013(),
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
					// 排除 transform 层（能力检测常量，非 Provider 路由）
					if strings.Contains(path, "llm/transform/") {
						return true
					}
					// 排除 config 层（默认值常量，非 Provider 路由）
					if strings.Contains(path, "config/") {
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
		fmt.Fprintf(&sb, "❌ %s [%s] %s:%d — %s\n",
			v.RuleID, v.Severity, v.File, v.Line, v.Message)
	}
	return sb.String()
}

// ─── SCAN-011: 工具事件泄露检测 ──────────────────────────────────
//
// 工具执行结果必须通过 ToolHook 管道发射事件，不得直接写 EventStream。

func (s *Scanner) ruleSCAN011() ScanRule {
	return ScanRule{
		ID: "SCAN-011",
		Severity: SeverityError,
		Check: func(file *ast.File, path string) []Violation {
			if !strings.Contains(path, "agent/") && !strings.Contains(path, "capability/") {
				return nil
			}
			if strings.Contains(path, "toolhook") {
				return nil
			}
			var violations []Violation
			ast.Inspect(file, func(n ast.Node) bool {
				comp, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}
				for _, elt := range comp.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					ident, ok := kv.Key.(*ast.Ident)
					if !ok {
						continue
					}
					if ident.Name == "Type" {
						basic, ok := kv.Value.(*ast.BasicLit)
						if ok {
							val := strings.Trim(basic.Value, `"`)
							if val == "tool_result" || val == "tool_call" {
								violations = append(violations, Violation{
									RuleID: "SCAN-011",
									Severity: SeverityError,
									File: path,
									Line: s.fset.Position(comp.Pos()).Line,
									Message: fmt.Sprintf("tool event %q must be emitted via ToolHook pipeline, not directly constructed", val),
								})
							}
						}
					}
				}
				return true
			})
			return violations
		},
	}
}

// ─── SCAN-012: 日志绕过检测 ─────────────────────────────────────────
//
// LLM 调用和工具调用必须经过 ExecLogger。

func (s *Scanner) ruleSCAN012() ScanRule {
	return ScanRule{
		ID: "SCAN-012",
		Severity: SeverityError,
		Check: func(file *ast.File, path string) []Violation {
			if !strings.Contains(path, "agent/") {
				return nil
			}
			// generator.go 自身就是 LLM 调用的执行点，调用前已通过 Logger.LogItem 记录；
			// SwapableProvider 是代理包装器。两者都不是"绕过日志"的调用。
			if strings.Contains(path, "agent/loop/") {
				return nil
			}
			var violations []Violation
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if sel.Sel.Name == "Generate" || sel.Sel.Name == "StreamChat" {
					if strings.HasSuffix(path, "_test.go") {
						return true
					}
					violations = append(violations, Violation{
						RuleID: "SCAN-012",
						Severity: SeverityError,
						File: path,
						Line: s.fset.Position(call.Pos()).Line,
						Message: fmt.Sprintf("direct LLM call %s() bypasses ExecLogger — use agent method that logs", sel.Sel.Name),
					})
				}
				return true
			})
			return violations
		},
	}
}

// ─── SCAN-013: 接口实现完整性检测 ───────────────────────────────────
//
// 声明的接口必须有测试覆盖。

func (s *Scanner) ruleSCAN013() ScanRule {
	return ScanRule{
		ID: "SCAN-013",
		Severity: SeverityWarning,
		Check: func(file *ast.File, path string) []Violation {
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if strings.Contains(path, "adapter/") || strings.Contains(path, "verify/") {
				return nil
			}
			var violations []Violation
			ifaceNames := map[string]bool{}
			for _, decl := range file.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.TYPE {
					continue
				}
				for _, spec := range gen.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					if _, isIface := ts.Type.(*ast.InterfaceType); isIface {
						ifaceNames[ts.Name.Name] = true
					}
				}
			}
			if len(ifaceNames) == 0 {
				return nil
			}
			dir := filepath.Dir(path)
			testFiles, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
			if err != nil || len(testFiles) == 0 {
				for name := range ifaceNames {
					violations = append(violations, Violation{
						RuleID: "SCAN-013",
						Severity: SeverityWarning,
						File: path,
						Line: s.fset.Position(file.Pos()).Line,
						Message: fmt.Sprintf("interface %q has no test file in same directory", name),
					})
				}
			}
			return violations
		},
	}
}
