// Package verify 的 CLI 入口：扫描项目并报告违规。
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/pengjunchen/go-agent-core/verify"
)

func main() {
	dir := flag.String("dir", ".", "要扫描的目录")
	flag.Parse()

	scanner := verify.NewScanner()
	violations, err := scanner.ScanDir(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan error: %v\n", err)
		os.Exit(2)
	}

	fmt.Println(verify.FormatViolations(violations))
	// 只有 Error 级别违规才返回非零退出码（Warning 不阻断）
	for _, v := range violations {
		if v.Severity == verify.SeverityError {
			os.Exit(1)
		}
	}
}
